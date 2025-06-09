package overwrite

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
)

// ObjectInfo contains S3 object metadata
type ObjectInfo struct {
	Bucket        string
	Key           string
	ContentType   *string
	ContentLength *int64
	ETag          *string
	LastModified  *time.Time
	Metadata      map[string]*string
	StorageClass  *string
	TagCount      *int64
	VersionId     *string
}

// OverwriteCallback defines the callback function signature
type OverwriteCallback func(info ObjectInfo, tmpFile *os.File) (bool, error)

// S3Client is the minimal interface required for overwrite operations
type S3Client interface {
	GetObject(input *s3.GetObjectInput) (*s3.GetObjectOutput, error)
	GetObjectTagging(input *s3.GetObjectTaggingInput) (*s3.GetObjectTaggingOutput, error)
	GetObjectAcl(input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error)
	PutObject(input *s3.PutObjectInput) (*s3.PutObjectOutput, error)
	PutObjectAcl(input *s3.PutObjectAclInput) (*s3.PutObjectAclOutput, error)
}

// OverwriteS3Object overwrites an S3 object while preserving its existing ACL
func OverwriteS3Object(
	client S3Client,
	bucket string,
	key string,
	callback OverwriteCallback,
) error {
	// Download object
	getResp, err := client.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to get object: %w", err)
	}
	defer func() {
		_ = getResp.Body.Close()
	}()

	// Create temporary file
	tmpFile, err := os.CreateTemp("", "s3-overwrite-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
	}()

	// Set file permissions to 0600
	if err := tmpFile.Chmod(0600); err != nil {
		return fmt.Errorf("failed to set temp file permissions: %w", err)
	}

	// Copy object content to temp file
	if _, err := io.Copy(tmpFile, getResp.Body); err != nil {
		return fmt.Errorf("failed to copy object content: %w", err)
	}

	// Seek to beginning for callback
	if _, err := tmpFile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek temp file: %w", err)
	}

	// Build ObjectInfo
	info := ObjectInfo{
		Bucket:        bucket,
		Key:           key,
		ContentType:   getResp.ContentType,
		ContentLength: getResp.ContentLength,
		ETag:          getResp.ETag,
		LastModified:  getResp.LastModified,
		Metadata:      getResp.Metadata,
		StorageClass:  getResp.StorageClass,
		TagCount:      getResp.TagCount,
		VersionId:     getResp.VersionId,
	}

	// Call callback
	shouldOverwrite, err := callback(info, tmpFile)
	if err != nil {
		return fmt.Errorf("callback error: %w", err)
	}

	if !shouldOverwrite {
		return nil
	}

	// Get existing tags
	var tagging *string
	if getResp.TagCount != nil && *getResp.TagCount > 0 {
		tagResp, err := client.GetObjectTagging(&s3.GetObjectTaggingInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			return fmt.Errorf("failed to get object tagging: %w", err)
		}

		if len(tagResp.TagSet) > 0 {
			tagStr := buildTaggingString(tagResp.TagSet)
			tagging = &tagStr
		}
	}

	// Get existing ACL
	aclResp, err := client.GetObjectAcl(&s3.GetObjectAclInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to get object ACL: %w", err)
	}

	// Seek to beginning for upload
	if _, err := tmpFile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek temp file for upload: %w", err)
	}

	// Build PutObject input
	putInput := &s3.PutObjectInput{
		Bucket:                  aws.String(bucket),
		Key:                     aws.String(key),
		Body:                    tmpFile,
		ContentType:             getResp.ContentType,
		CacheControl:            getResp.CacheControl,
		ContentDisposition:      getResp.ContentDisposition,
		ContentEncoding:         getResp.ContentEncoding,
		ContentLanguage:         getResp.ContentLanguage,
		WebsiteRedirectLocation: getResp.WebsiteRedirectLocation,
		StorageClass:            getResp.StorageClass,
		Metadata:                info.Metadata, // Use metadata from callback-modified info
		Tagging:                 tagging,
	}

	// Add grant parameters (except WRITE)
	addGrantsToInput(putInput, aclResp.Grants, false)

	// Put object
	if _, err := client.PutObject(putInput); err != nil {
		return fmt.Errorf("failed to put object: %w", err)
	}

	// Check if we need to restore WRITE permissions
	if hasWriteGrant(aclResp.Grants) {
		aclInput := &s3.PutObjectAclInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		}
		addGrantsToInput(aclInput, aclResp.Grants, true)

		if _, err := client.PutObjectAcl(aclInput); err != nil {
			return fmt.Errorf("failed to put object ACL: %w", err)
		}
	}

	return nil
}

// OverwriteS3ObjectWithAcl overwrites an S3 object with a specific simple ACL
func OverwriteS3ObjectWithAcl(
	client S3Client,
	bucket string,
	key string,
	acl string,
	callback OverwriteCallback,
) error {
	// Download object
	getResp, err := client.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to get object: %w", err)
	}
	defer func() {
		_ = getResp.Body.Close()
	}()

	// Create temporary file
	tmpFile, err := os.CreateTemp("", "s3-overwrite-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
	}()

	// Set file permissions to 0600
	if err := tmpFile.Chmod(0600); err != nil {
		return fmt.Errorf("failed to set temp file permissions: %w", err)
	}

	// Copy object content to temp file
	if _, err := io.Copy(tmpFile, getResp.Body); err != nil {
		return fmt.Errorf("failed to copy object content: %w", err)
	}

	// Seek to beginning for callback
	if _, err := tmpFile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek temp file: %w", err)
	}

	// Build ObjectInfo
	info := ObjectInfo{
		Bucket:        bucket,
		Key:           key,
		ContentType:   getResp.ContentType,
		ContentLength: getResp.ContentLength,
		ETag:          getResp.ETag,
		LastModified:  getResp.LastModified,
		Metadata:      getResp.Metadata,
		StorageClass:  getResp.StorageClass,
		TagCount:      getResp.TagCount,
		VersionId:     getResp.VersionId,
	}

	// Call callback
	shouldOverwrite, err := callback(info, tmpFile)
	if err != nil {
		return fmt.Errorf("callback error: %w", err)
	}

	if !shouldOverwrite {
		return nil
	}

	// Get existing tags
	var tagging *string
	if getResp.TagCount != nil && *getResp.TagCount > 0 {
		tagResp, err := client.GetObjectTagging(&s3.GetObjectTaggingInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			return fmt.Errorf("failed to get object tagging: %w", err)
		}

		if len(tagResp.TagSet) > 0 {
			tagStr := buildTaggingString(tagResp.TagSet)
			tagging = &tagStr
		}
	}

	// Seek to beginning for upload
	if _, err := tmpFile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek temp file for upload: %w", err)
	}

	// Build PutObject input with simple ACL
	putInput := &s3.PutObjectInput{
		Bucket:                  aws.String(bucket),
		Key:                     aws.String(key),
		Body:                    tmpFile,
		ACL:                     aws.String(acl),
		ContentType:             getResp.ContentType,
		CacheControl:            getResp.CacheControl,
		ContentDisposition:      getResp.ContentDisposition,
		ContentEncoding:         getResp.ContentEncoding,
		ContentLanguage:         getResp.ContentLanguage,
		WebsiteRedirectLocation: getResp.WebsiteRedirectLocation,
		StorageClass:            getResp.StorageClass,
		Metadata:                info.Metadata, // Use metadata from callback-modified info
		Tagging:                 tagging,
	}

	// Put object
	if _, err := client.PutObject(putInput); err != nil {
		return fmt.Errorf("failed to put object: %w", err)
	}

	return nil
}

// buildTaggingString converts S3 tags to query string format
func buildTaggingString(tags []*s3.Tag) string {
	if len(tags) == 0 {
		return ""
	}

	var result string
	for i, tag := range tags {
		if i > 0 {
			result += "&"
		}
		result += url.QueryEscape(aws.StringValue(tag.Key)) + "=" + url.QueryEscape(aws.StringValue(tag.Value))
	}
	return result
}

// addGrantsToInput adds grant parameters to PutObject or PutObjectAcl input
func addGrantsToInput(input interface{}, grants []*s3.Grant, includeWrite bool) {
	readGrants := buildGrantString(grants, "READ")
	readAcpGrants := buildGrantString(grants, "READ_ACP")
	writeAcpGrants := buildGrantString(grants, "WRITE_ACP")
	fullControlGrants := buildGrantString(grants, "FULL_CONTROL")

	switch v := input.(type) {
	case *s3.PutObjectInput:
		if readGrants != "" {
			v.GrantRead = &readGrants
		}
		if readAcpGrants != "" {
			v.GrantReadACP = &readAcpGrants
		}
		if writeAcpGrants != "" {
			v.GrantWriteACP = &writeAcpGrants
		}
		if fullControlGrants != "" {
			v.GrantFullControl = &fullControlGrants
		}
	case *s3.PutObjectAclInput:
		if readGrants != "" {
			v.GrantRead = &readGrants
		}
		if readAcpGrants != "" {
			v.GrantReadACP = &readAcpGrants
		}
		if writeAcpGrants != "" {
			v.GrantWriteACP = &writeAcpGrants
		}
		if fullControlGrants != "" {
			v.GrantFullControl = &fullControlGrants
		}
		if includeWrite {
			writeGrants := buildGrantString(grants, "WRITE")
			if writeGrants != "" {
				v.GrantWrite = &writeGrants
			}
		}
	}
}

// buildGrantString builds grant string for specific permission
func buildGrantString(grants []*s3.Grant, permission string) string {
	var grantees []string
	for _, grant := range grants {
		if grant.Permission != nil && *grant.Permission == permission {
			if grant.Grantee.ID != nil {
				grantees = append(grantees, fmt.Sprintf("id=\"%s\"", *grant.Grantee.ID))
			} else if grant.Grantee.URI != nil {
				grantees = append(grantees, fmt.Sprintf("uri=\"%s\"", *grant.Grantee.URI))
			} else if grant.Grantee.EmailAddress != nil {
				grantees = append(grantees, fmt.Sprintf("emailaddress=\"%s\"", *grant.Grantee.EmailAddress))
			}
		}
	}
	if len(grantees) == 0 {
		return ""
	}
	return strings.Join(grantees, ",") // Join multiple grantees with comma
}

// hasWriteGrant checks if grants contain WRITE permission
func hasWriteGrant(grants []*s3.Grant) bool {
	for _, grant := range grants {
		if grant.Permission != nil && *grant.Permission == "WRITE" {
			return true
		}
	}
	return false
}