package overwrite

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
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
// The callback receives object info and the path to the source file.
// It returns:
// - overwritingFilePath: the path to the file to upload (empty string to skip overwrite)
// - autoRemove: if true, the file will be automatically removed after upload (only if different from srcFilePath)
// - err: any error that occurred
type OverwriteCallback func(info ObjectInfo, srcFilePath string) (overwritingFilePath string, autoRemove bool, err error)

// S3Client is the minimal interface required for overwrite operations
type S3Client interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	GetObjectTagging(ctx context.Context, params *s3.GetObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.GetObjectTaggingOutput, error)
	GetObjectAcl(ctx context.Context, params *s3.GetObjectAclInput, optFns ...func(*s3.Options)) (*s3.GetObjectAclOutput, error)
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	PutObjectAcl(ctx context.Context, params *s3.PutObjectAclInput, optFns ...func(*s3.Options)) (*s3.PutObjectAclOutput, error)
}

// OverwriteS3Object overwrites an S3 object while preserving its existing ACL
func OverwriteS3Object(
	ctx context.Context,
	client S3Client,
	bucket string,
	key string,
	callback OverwriteCallback,
) error {
	// Download object
	getResp, err := client.GetObject(ctx, &s3.GetObjectInput{
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
		Metadata:      convertMetadataToPointers(getResp.Metadata),
		StorageClass:  aws.String(string(getResp.StorageClass)),
		TagCount:      aws.Int64(int64(aws.ToInt32(getResp.TagCount))),
		VersionId:     getResp.VersionId,
	}

	// Call callback with temp file path
	overwritingFilePath, autoRemove, err := callback(info, tmpFile.Name())
	if err != nil {
		return fmt.Errorf("callback error: %w", err)
	}

	if overwritingFilePath == "" {
		return nil
	}

	// Schedule cleanup if requested
	if autoRemove && overwritingFilePath != "" && overwritingFilePath != tmpFile.Name() {
		defer func() {
			_ = os.Remove(overwritingFilePath)
		}()
	}

	// Get existing tags
	var tagging *string
	if getResp.TagCount != nil && *getResp.TagCount > 0 {
		tagResp, err := client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
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
	aclResp, err := client.GetObjectAcl(ctx, &s3.GetObjectAclInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to get object ACL: %w", err)
	}

	// Open the file to upload
	uploadFile, err := os.Open(overwritingFilePath)
	if err != nil {
		return fmt.Errorf("failed to open overwriting file: %w", err)
	}
	defer uploadFile.Close()

	// Build PutObject input
	putInput := &s3.PutObjectInput{
		Bucket:                  aws.String(bucket),
		Key:                     aws.String(key),
		Body:                    uploadFile,
		ContentType:             getResp.ContentType,
		CacheControl:            getResp.CacheControl,
		ContentDisposition:      getResp.ContentDisposition,
		ContentEncoding:         getResp.ContentEncoding,
		ContentLanguage:         getResp.ContentLanguage,
		WebsiteRedirectLocation: getResp.WebsiteRedirectLocation,
		StorageClass:            getResp.StorageClass,
		Metadata:                convertMetadataFromPointers(info.Metadata), // Use metadata from callback-modified info
		Tagging:                 tagging,
	}

	// Add grant parameters (except WRITE)
	addGrantsToInput(putInput, aclResp.Grants, false)

	// Put object
	if _, err := client.PutObject(ctx, putInput); err != nil {
		return fmt.Errorf("failed to put object: %w", err)
	}

	// Check if we need to restore WRITE permissions
	if hasWriteGrant(aclResp.Grants) {
		aclInput := &s3.PutObjectAclInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		}
		addGrantsToInput(aclInput, aclResp.Grants, true)

		if _, err := client.PutObjectAcl(ctx, aclInput); err != nil {
			return fmt.Errorf("failed to put object ACL: %w", err)
		}
	}

	return nil
}

// OverwriteS3ObjectWithAcl overwrites an S3 object with a specific simple ACL
func OverwriteS3ObjectWithAcl(
	ctx context.Context,
	client S3Client,
	bucket string,
	key string,
	acl string,
	callback OverwriteCallback,
) error {
	// Download object
	getResp, err := client.GetObject(ctx, &s3.GetObjectInput{
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
		Metadata:      convertMetadataToPointers(getResp.Metadata),
		StorageClass:  aws.String(string(getResp.StorageClass)),
		TagCount:      aws.Int64(int64(aws.ToInt32(getResp.TagCount))),
		VersionId:     getResp.VersionId,
	}

	// Call callback with temp file path
	overwritingFilePath, autoRemove, err := callback(info, tmpFile.Name())
	if err != nil {
		return fmt.Errorf("callback error: %w", err)
	}

	if overwritingFilePath == "" {
		return nil
	}

	// Schedule cleanup if requested
	if autoRemove && overwritingFilePath != "" && overwritingFilePath != tmpFile.Name() {
		defer func() {
			_ = os.Remove(overwritingFilePath)
		}()
	}

	// Get existing tags
	var tagging *string
	if getResp.TagCount != nil && *getResp.TagCount > 0 {
		tagResp, err := client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
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

	// Open the file to upload
	uploadFile, err := os.Open(overwritingFilePath)
	if err != nil {
		return fmt.Errorf("failed to open overwriting file: %w", err)
	}
	defer uploadFile.Close()

	// Build PutObject input with simple ACL
	putInput := &s3.PutObjectInput{
		Bucket:                  aws.String(bucket),
		Key:                     aws.String(key),
		Body:                    uploadFile,
		ACL:                     types.ObjectCannedACL(acl),
		ContentType:             getResp.ContentType,
		CacheControl:            getResp.CacheControl,
		ContentDisposition:      getResp.ContentDisposition,
		ContentEncoding:         getResp.ContentEncoding,
		ContentLanguage:         getResp.ContentLanguage,
		WebsiteRedirectLocation: getResp.WebsiteRedirectLocation,
		StorageClass:            getResp.StorageClass,
		Metadata:                convertMetadataFromPointers(info.Metadata), // Use metadata from callback-modified info
		Tagging:                 tagging,
	}

	// Put object
	if _, err := client.PutObject(ctx, putInput); err != nil {
		return fmt.Errorf("failed to put object: %w", err)
	}

	return nil
}

// buildTaggingString converts S3 tags to query string format
func buildTaggingString(tags []types.Tag) string {
	if len(tags) == 0 {
		return ""
	}

	var result string
	for i, tag := range tags {
		if i > 0 {
			result += "&"
		}
		result += url.QueryEscape(aws.ToString(tag.Key)) + "=" + url.QueryEscape(aws.ToString(tag.Value))
	}
	return result
}

// addGrantsToInput adds grant parameters to PutObject or PutObjectAcl input
func addGrantsToInput(input interface{}, grants []types.Grant, includeWrite bool) {
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
func buildGrantString(grants []types.Grant, permission string) string {
	var grantees []string
	for _, grant := range grants {
		if string(grant.Permission) == permission {
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
func hasWriteGrant(grants []types.Grant) bool {
	for _, grant := range grants {
		if grant.Permission == types.PermissionWrite {
			return true
		}
	}
	return false
}

// convertMetadataToPointers converts map[string]string to map[string]*string
func convertMetadataToPointers(metadata map[string]string) map[string]*string {
	if metadata == nil {
		return nil
	}
	result := make(map[string]*string)
	for k, v := range metadata {
		val := v
		result[k] = &val
	}
	return result
}

// convertMetadataFromPointers converts map[string]*string to map[string]string
func convertMetadataFromPointers(metadata map[string]*string) map[string]string {
	if metadata == nil {
		return nil
	}
	result := make(map[string]string)
	for k, v := range metadata {
		if v != nil {
			result[k] = *v
		}
	}
	return result
}