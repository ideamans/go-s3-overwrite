//go:build e2e
// +build e2e

package overwrite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func getTestS3Client(t *testing.T) *s3.Client {
	// Check for required environment variables
	bucket := os.Getenv("TEST_BUCKET")
	if bucket == "" {
		t.Skip("TEST_BUCKET not set, skipping E2E tests")
	}

	// Create AWS configuration
	var opts []func(*config.LoadOptions) error

	// Set region if provided, default to ap-northeast-1
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "ap-northeast-1"
	}
	opts = append(opts, config.WithRegion(region))

	// Use explicit credentials if provided
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	if accessKey != "" && secretKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}

	// Load configuration
	cfg, err := config.LoadDefaultConfig(context.TODO(), opts...)
	if err != nil {
		t.Fatalf("Failed to create AWS config: %v", err)
	}

	return s3.NewFromConfig(cfg)
}

func TestE2E_OverwriteS3Object(t *testing.T) {
	client := getTestS3Client(t)
	bucket := os.Getenv("TEST_BUCKET")
	key := fmt.Sprintf("go-s3-overwrite-test/%d/test.txt", time.Now().Unix())

	// Initial upload with tags and metadata
	_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:       aws.String(bucket),
		Key:          aws.String(key),
		Body:         strings.NewReader("original content"),
		ContentType:  aws.String("text/plain"),
		CacheControl: aws.String("max-age=3600"),
		Tagging:      aws.String("env=test&purpose=e2e-test"),
		Metadata: map[string]string{
			"original": "true",
			"created":  time.Now().Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatalf("Failed to upload test object: %v", err)
	}

	// Set ACL with multiple grants
	_, err = client.PutObjectAcl(context.Background(), &s3.PutObjectAclInput{
		Bucket:       aws.String(bucket),
		Key:          aws.String(key),
		GrantRead:    aws.String("uri=\"http://acs.amazonaws.com/groups/global/AllUsers\""),
		GrantReadACP: aws.String("uri=\"http://acs.amazonaws.com/groups/global/AuthenticatedUsers\""),
	})
	if err != nil {
		t.Fatalf("Failed to set ACL: %v", err)
	}

	// Cleanup
	defer client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	// Test overwrite with ACL preservation
	err = OverwriteS3Object(context.Background(), client, bucket, key, func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		// Verify original content
		content, err := os.ReadFile(srcFilePath)
		if err != nil {
			return "", false, err
		}
		if string(content) != "original content" {
			t.Errorf("Expected 'original content', got '%s'", string(content))
		}

		// Verify metadata (check both lowercase and capitalized keys)
		foundOriginal := false
		if val, ok := info.Metadata["original"]; ok && *val == "true" {
			foundOriginal = true
		}
		if val, ok := info.Metadata["Original"]; ok && *val == "true" {
			foundOriginal = true
		}
		if !foundOriginal {
			t.Errorf("Original metadata not found in callback, got metadata: %v", info.Metadata)
		}

		// Write modified content to a new file
		modifiedFile, err := os.CreateTemp("", "e2e-modified-*.tmp")
		if err != nil {
			return "", false, err
		}
		defer modifiedFile.Close()
		
		if _, err := modifiedFile.WriteString("modified content via E2E test"); err != nil {
			os.Remove(modifiedFile.Name())
			return "", false, err
		}

		// Add new metadata
		info.Metadata["modified"] = aws.String("true")
		info.Metadata["modified-at"] = aws.String(time.Now().Format(time.RFC3339))

		return modifiedFile.Name(), true, nil
	})

	if err != nil {
		t.Fatalf("Failed to overwrite object: %v", err)
	}

	// Verify the object was updated correctly
	getResp, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get object after overwrite: %v", err)
	}
	defer getResp.Body.Close()

	// Check content
	newContent, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("Failed to read object content: %v", err)
	}
	if string(newContent) != "modified content via E2E test" {
		t.Errorf("Expected 'modified content via E2E test', got '%s'", string(newContent))
	}

	// Check preserved attributes
	if *getResp.ContentType != "text/plain" {
		t.Errorf("ContentType not preserved, got %s", *getResp.ContentType)
	}
	if *getResp.CacheControl != "max-age=3600" {
		t.Errorf("CacheControl not preserved, got %s", *getResp.CacheControl)
	}

	// Check metadata (S3 returns metadata keys with capital first letter)
	if val, ok := getResp.Metadata["Original"]; !ok || val != "true" {
		t.Errorf("Original metadata not preserved, got metadata: %v", getResp.Metadata)
	}
	if val, ok := getResp.Metadata["Modified"]; !ok || val != "true" {
		t.Errorf("Modified metadata not added, got metadata: %v", getResp.Metadata)
	}

	// Check tags
	tagResp, err := client.GetObjectTagging(context.Background(), &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get tags: %v", err)
	}

	expectedTags := map[string]string{
		"env":     "test",
		"purpose": "e2e-test",
	}
	for _, tag := range tagResp.TagSet {
		if expected, ok := expectedTags[*tag.Key]; ok {
			if *tag.Value != expected {
				t.Errorf("Tag %s: expected '%s', got '%s'", *tag.Key, expected, *tag.Value)
			}
			delete(expectedTags, *tag.Key)
		}
	}
	if len(expectedTags) > 0 {
		t.Errorf("Missing tags: %v", expectedTags)
	}

	// Check ACL
	aclResp, err := client.GetObjectAcl(context.Background(), &s3.GetObjectAclInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get ACL: %v", err)
	}

	// Verify ACL grants are preserved
	hasPublicRead := false
	hasAuthenticatedReadACP := false
	for _, grant := range aclResp.Grants {
		if grant.Grantee.URI != nil {
			if *grant.Grantee.URI == "http://acs.amazonaws.com/groups/global/AllUsers" && string(grant.Permission) == "READ" {
				hasPublicRead = true
			}
			if *grant.Grantee.URI == "http://acs.amazonaws.com/groups/global/AuthenticatedUsers" && string(grant.Permission) == "READ_ACP" {
				hasAuthenticatedReadACP = true
			}
		}
	}
	if !hasPublicRead {
		t.Error("Public read ACL not preserved")
	}
	if !hasAuthenticatedReadACP {
		t.Error("Authenticated read ACP not preserved")
	}
	// Note: S3 doesn't always return owner's FULL_CONTROL explicitly in GetObjectAcl
	// The owner always has FULL_CONTROL implicitly
}

func TestE2E_OverwriteS3ObjectWithAcl(t *testing.T) {
	client := getTestS3Client(t)
	bucket := os.Getenv("TEST_BUCKET")
	key := fmt.Sprintf("go-s3-overwrite-test/%d/json-test.json", time.Now().Unix())

	// Upload initial JSON
	initialData := map[string]interface{}{
		"name":    "test",
		"version": 1,
		"created": time.Now().Format(time.RFC3339),
	}
	jsonData, _ := json.Marshal(initialData)

	_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:       aws.String(bucket),
		Key:          aws.String(key),
		Body:         strings.NewReader(string(jsonData)),
		ContentType:  aws.String("application/json"),
		ACL:          types.ObjectCannedACLPublicRead,
		Tagging:      aws.String("type=json&format=unformatted"),
		CacheControl: aws.String("no-cache"),
		Metadata: map[string]string{
			"app": "go-s3-overwrite",
		},
	})
	if err != nil {
		t.Fatalf("Failed to upload test JSON: %v", err)
	}

	// Cleanup
	defer client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	// Test overwrite preserving private ACL
	err = OverwriteS3ObjectWithAcl(context.Background(), client, bucket, key, "private", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		// Read and parse JSON
		data, err := os.ReadFile(srcFilePath)
		if err != nil {
			return "", false, err
		}

		var jsonObj map[string]interface{}
		if err := json.Unmarshal(data, &jsonObj); err != nil {
			return "", false, fmt.Errorf("failed to parse JSON: %w", err)
		}

		// Update JSON
		jsonObj["version"] = 2
		jsonObj["modified"] = time.Now().Format(time.RFC3339)
		jsonObj["modifier"] = "E2E test"

		// Format with indentation
		formatted, err := json.MarshalIndent(jsonObj, "", "  ")
		if err != nil {
			return "", false, err
		}

		// Write to new file
		formattedFile, err := os.CreateTemp("", "e2e-json-*.json")
		if err != nil {
			return "", false, err
		}
		defer formattedFile.Close()
		
		if _, err := formattedFile.Write(formatted); err != nil {
			os.Remove(formattedFile.Name())
			return "", false, err
		}

		// Update metadata
		info.Metadata["formatted"] = aws.String("true")

		return formattedFile.Name(), true, nil
	})

	if err != nil {
		t.Fatalf("Failed to overwrite JSON: %v", err)
	}

	// Verify the result
	getResp, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get object after overwrite: %v", err)
	}
	defer getResp.Body.Close()

	// Check content
	newContent, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("Failed to read object content: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(newContent, &result); err != nil {
		t.Fatalf("Failed to parse result JSON: %v", err)
	}

	if result["version"].(float64) != 2 {
		t.Errorf("Expected version 2, got %v", result["version"])
	}
	if _, ok := result["modified"]; !ok {
		t.Error("Modified field not added")
	}
	if result["modifier"] != "E2E test" {
		t.Errorf("Expected modifier 'E2E test', got %v", result["modifier"])
	}

	// Check formatting (should have indentation)
	if !strings.Contains(string(newContent), "\n  ") {
		t.Error("JSON not properly formatted with indentation")
	}

	// Check ACL changed to private
	aclResp, err := client.GetObjectAcl(context.Background(), &s3.GetObjectAclInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get ACL: %v", err)
	}

	// Should only have owner permissions
	for _, grant := range aclResp.Grants {
		if grant.Grantee.URI != nil {
			t.Errorf("Found unexpected public grant: %s", *grant.Grantee.URI)
		}
	}

	// Check tags preserved
	tagResp, err := client.GetObjectTagging(context.Background(), &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get tags: %v", err)
	}

	if len(tagResp.TagSet) != 2 {
		t.Errorf("Expected 2 tags, got %d", len(tagResp.TagSet))
	}
}

func TestE2E_SkipLargeFiles(t *testing.T) {
	client := getTestS3Client(t)
	bucket := os.Getenv("TEST_BUCKET")
	key := fmt.Sprintf("go-s3-overwrite-test/%d/skip-test.txt", time.Now().Unix())

	// Upload test object
	content := "This file should be skipped"
	_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:  aws.String(bucket),
		Key:     aws.String(key),
		Body:    strings.NewReader(content),
		Tagging: aws.String("test=skip"),
	})
	if err != nil {
		t.Fatalf("Failed to upload test object: %v", err)
	}

	// Cleanup
	defer client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	callbackCalled := false
	err = OverwriteS3Object(context.Background(), client, bucket, key, func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		callbackCalled = true

		// Simulate size check - skip if content length > 10 bytes
		if *info.ContentLength > 10 {
			return "", false, nil // Skip
		}

		t.Error("Should have skipped this file")
		return srcFilePath, false, nil
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !callbackCalled {
		t.Error("Callback was not called")
	}

	// Verify content unchanged
	getResp, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get object: %v", err)
	}
	defer getResp.Body.Close()

	data, _ := io.ReadAll(getResp.Body)
	if string(data) != content {
		t.Error("Content was modified when it should have been skipped")
	}
}

func TestE2E_ErrorHandling(t *testing.T) {
	client := getTestS3Client(t)
	bucket := os.Getenv("TEST_BUCKET")

	// Test with non-existent object
	err := OverwriteS3Object(context.Background(), client, bucket, "non-existent-key", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		t.Error("Callback should not be called for non-existent object")
		return srcFilePath, false, nil
	})

	if err == nil {
		t.Error("Expected error for non-existent object")
	}

	// Test callback error
	key := fmt.Sprintf("go-s3-overwrite-test/%d/error-test.txt", time.Now().Unix())
	_, err = client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader("test"),
	})
	if err != nil {
		t.Fatalf("Failed to upload test object: %v", err)
	}

	defer client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	err = OverwriteS3Object(context.Background(), client, bucket, key, func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		return "", false, fmt.Errorf("simulated callback error")
	})

	if err == nil {
		t.Error("Expected error from callback")
	}
	if !strings.Contains(err.Error(), "simulated callback error") {
		t.Errorf("Expected callback error, got: %v", err)
	}
}

func TestE2E_TempFileCleanup(t *testing.T) {
	client := getTestS3Client(t)
	bucket := os.Getenv("TEST_BUCKET")
	key := fmt.Sprintf("go-s3-overwrite-test/%d/cleanup-test.txt", time.Now().Unix())

	// Upload test object
	_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader("test content for cleanup"),
	})
	if err != nil {
		t.Fatalf("Failed to upload test object: %v", err)
	}

	defer client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	var tempFiles []string

	// Test successful case
	err = OverwriteS3Object(context.Background(), client, bucket, key, func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		tempFiles = append(tempFiles, srcFilePath)
		return srcFilePath, false, nil
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check temp files are cleaned up
	for _, tf := range tempFiles {
		if _, err := os.Stat(tf); !os.IsNotExist(err) {
			t.Errorf("Temporary file %s was not cleaned up", tf)
		}
	}

	// Test error case
	tempFiles = nil
	err = OverwriteS3Object(context.Background(), client, bucket, key, func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		tempFiles = append(tempFiles, srcFilePath)
		return "", false, fmt.Errorf("force cleanup test")
	})

	if err == nil {
		t.Error("Expected error")
	}

	// Check temp files are cleaned up even on error
	for _, tf := range tempFiles {
		if _, err := os.Stat(tf); !os.IsNotExist(err) {
			t.Errorf("Temporary file %s was not cleaned up after error", tf)
		}
	}
}

func TestE2E_ComplexACLPreservation(t *testing.T) {
	client := getTestS3Client(t)
	bucket := os.Getenv("TEST_BUCKET")
	key := fmt.Sprintf("go-s3-overwrite-test/%d/complex-acl.txt", time.Now().Unix())

	// Upload object
	_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        strings.NewReader("test content"),
		ContentType: aws.String("text/plain"),
	})
	if err != nil {
		t.Fatalf("Failed to upload test object: %v", err)
	}

	// Set complex ACL with multiple grants
	_, err = client.PutObjectAcl(context.Background(), &s3.PutObjectAclInput{
		Bucket:           aws.String(bucket),
		Key:              aws.String(key),
		GrantRead:        aws.String("uri=\"http://acs.amazonaws.com/groups/global/AllUsers\""),
		GrantWrite:       aws.String("uri=\"http://acs.amazonaws.com/groups/global/AuthenticatedUsers\""),
		GrantReadACP:     aws.String("uri=\"http://acs.amazonaws.com/groups/global/AuthenticatedUsers\""),
		GrantWriteACP:    aws.String("uri=\"http://acs.amazonaws.com/groups/global/AuthenticatedUsers\""),
		GrantFullControl: aws.String("uri=\"http://acs.amazonaws.com/groups/s3/LogDelivery\""),
	})
	if err != nil {
		t.Fatalf("Failed to set complex ACL: %v", err)
	}

	// Cleanup
	defer client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	// Test overwrite with ACL preservation
	err = OverwriteS3Object(context.Background(), client, bucket, key, func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		// Modify content to new file
		modifiedFile, err := os.CreateTemp("", "e2e-complex-acl-*.txt")
		if err != nil {
			return "", false, err
		}
		defer modifiedFile.Close()
		
		if _, err := modifiedFile.WriteString("modified content with complex ACL"); err != nil {
			os.Remove(modifiedFile.Name())
			return "", false, err
		}
		
		return modifiedFile.Name(), true, nil
	})

	if err != nil {
		t.Fatalf("Failed to overwrite object: %v", err)
	}

	// Verify ACL is fully preserved
	aclResp, err := client.GetObjectAcl(context.Background(), &s3.GetObjectAclInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get ACL: %v", err)
	}

	// Check all permissions
	expectedPermissions := map[string]map[string]bool{
		"http://acs.amazonaws.com/groups/global/AllUsers": {
			"READ": false,
		},
		"http://acs.amazonaws.com/groups/global/AuthenticatedUsers": {
			"WRITE":     false,
			"READ_ACP":  false,
			"WRITE_ACP": false,
		},
		"http://acs.amazonaws.com/groups/s3/LogDelivery": {
			"FULL_CONTROL": false,
		},
	}

	for _, grant := range aclResp.Grants {
		if grant.Grantee.URI != nil {
			uri := *grant.Grantee.URI
			perm := string(grant.Permission)
			if perms, ok := expectedPermissions[uri]; ok {
				if _, ok := perms[perm]; ok {
					perms[perm] = true
				}
			}
		}
	}

	// Verify all expected permissions were found
	for uri, perms := range expectedPermissions {
		for perm, found := range perms {
			if !found {
				t.Errorf("Expected %s permission for %s not found", perm, uri)
			}
		}
	}
}

func TestE2E_PublicPrivateACLSwitch(t *testing.T) {
	client := getTestS3Client(t)
	bucket := os.Getenv("TEST_BUCKET")
	key := fmt.Sprintf("go-s3-overwrite-test/%d/acl-switch.txt", time.Now().Unix())

	// Start with public-read
	_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        strings.NewReader("public content"),
		ContentType: aws.String("text/plain"),
		ACL:         types.ObjectCannedACLPublicRead,
		Tagging:     aws.String("visibility=public"),
	})
	if err != nil {
		t.Fatalf("Failed to upload public object: %v", err)
	}

	// Cleanup
	defer client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	// Switch to private
	err = OverwriteS3ObjectWithAcl(context.Background(), client, bucket, key, "private", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		// Update content
		modifiedFile, err := os.CreateTemp("", "e2e-private-*.txt")
		if err != nil {
			return "", false, err
		}
		defer modifiedFile.Close()
		
		if _, err := modifiedFile.WriteString("now private content"); err != nil {
			os.Remove(modifiedFile.Name())
			return "", false, err
		}
		
		return modifiedFile.Name(), true, nil
	})

	if err != nil {
		t.Fatalf("Failed to switch to private: %v", err)
	}

	// Verify it's private
	aclResp, err := client.GetObjectAcl(context.Background(), &s3.GetObjectAclInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get ACL: %v", err)
	}

	for _, grant := range aclResp.Grants {
		if grant.Grantee.URI != nil && *grant.Grantee.URI == "http://acs.amazonaws.com/groups/global/AllUsers" {
			t.Error("Object should be private but has public access")
		}
	}

	// Verify tags were preserved
	tagResp, err := client.GetObjectTagging(context.Background(), &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get tags: %v", err)
	}

	foundTag := false
	for _, tag := range tagResp.TagSet {
		if *tag.Key == "visibility" && *tag.Value == "public" {
			foundTag = true
		}
	}
	if !foundTag {
		t.Error("Original tag not preserved during ACL switch")
	}

	// Switch back to public-read
	err = OverwriteS3ObjectWithAcl(context.Background(), client, bucket, key, "public-read", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		// Change content again
		modifiedFile, err := os.CreateTemp("", "e2e-public-*.txt")
		if err != nil {
			return "", false, err
		}
		defer modifiedFile.Close()
		
		if _, err := modifiedFile.WriteString("public again"); err != nil {
			os.Remove(modifiedFile.Name())
			return "", false, err
		}
		
		return modifiedFile.Name(), true, nil
	})

	if err != nil {
		t.Fatalf("Failed to switch back to public: %v", err)
	}

	// Verify it's public again
	aclResp, err = client.GetObjectAcl(context.Background(), &s3.GetObjectAclInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get ACL: %v", err)
	}

	hasPublicRead := false
	for _, grant := range aclResp.Grants {
		if grant.Grantee.URI != nil && *grant.Grantee.URI == "http://acs.amazonaws.com/groups/global/AllUsers" && string(grant.Permission) == "READ" {
			hasPublicRead = true
		}
	}
	if !hasPublicRead {
		t.Error("Object should have public-read access")
	}
}

func TestE2E_MetadataAndTagsWithSpecialCharacters(t *testing.T) {
	client := getTestS3Client(t)
	bucket := os.Getenv("TEST_BUCKET")
	key := fmt.Sprintf("go-s3-overwrite-test/%d/special-chars.txt", time.Now().Unix())

	// Upload with special characters in metadata and tags
	_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        strings.NewReader("test content"),
		ContentType: aws.String("text/plain; charset=utf-8"),
		Tagging:     aws.String("env=test&purpose=e2e-test&special=%E3%83%86%E3%82%B9%E3%83%88"), // Japanese "テスト"
		Metadata: map[string]string{
			"user-name":     "John Doe",
			"special-chars": "!@#$%^&*()_+-=",
			"unicode":       "こんにちは", // Japanese "Hello"
		},
	})
	if err != nil {
		t.Fatalf("Failed to upload object: %v", err)
	}

	// Cleanup
	defer client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	// Overwrite while preserving special characters
	err = OverwriteS3Object(context.Background(), client, bucket, key, func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		// Add more metadata
		info.Metadata["path"] = aws.String("/path/to/file")
		info.Metadata["status"] = aws.String("processed")

		// Update content
		modifiedFile, err := os.CreateTemp("", "e2e-metadata-*.txt")
		if err != nil {
			return "", false, err
		}
		defer modifiedFile.Close()
		
		if _, err := modifiedFile.WriteString("updated content with special metadata"); err != nil {
			os.Remove(modifiedFile.Name())
			return "", false, err
		}
		
		return modifiedFile.Name(), true, nil
	})

	if err != nil {
		t.Fatalf("Failed to overwrite: %v", err)
	}

	// Verify metadata
	getResp, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get object: %v", err)
	}
	defer getResp.Body.Close()

	// Check special character metadata
	expectedMetadata := map[string]string{
		"User-Name":     "John Doe",
		"Special-Chars": "!@#$%^&*()_+-=",
		"Path":          "/path/to/file",
		"Status":        "processed",
	}

	for key, expected := range expectedMetadata {
		if val, ok := getResp.Metadata[key]; !ok {
			t.Errorf("Metadata %s not found", key)
		} else if val != expected {
			t.Errorf("Metadata %s: expected '%s', got '%s'", key, expected, val)
		}
	}

	// Verify tags with special characters
	tagResp, err := client.GetObjectTagging(context.Background(), &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get tags: %v", err)
	}

	foundSpecialTag := false
	for _, tag := range tagResp.TagSet {
		if *tag.Key == "special" && *tag.Value == "テスト" {
			foundSpecialTag = true
		}
	}
	if !foundSpecialTag {
		t.Error("Special character tag not preserved")
	}
}

func TestE2E_LargeMetadataAndManyTags(t *testing.T) {
	client := getTestS3Client(t)
	bucket := os.Getenv("TEST_BUCKET")
	key := fmt.Sprintf("go-s3-overwrite-test/%d/many-attrs.txt", time.Now().Unix())

	// Create many tags (S3 limit is 10 tags)
	tags := []string{}
	for i := 0; i < 10; i++ {
		tags = append(tags, fmt.Sprintf("tag%d=value%d", i, i))
	}
	tagString := strings.Join(tags, "&")

	// Create metadata with long values
	longValue := strings.Repeat("a", 1000) // S3 metadata value limit is 2KB
	metadata := map[string]string{}
	for i := 0; i < 5; i++ {
		metadata[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value-%d-%s", i, longValue[:100])
	}

	// Upload with many attributes
	_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		Body:     strings.NewReader("test content"),
		Tagging:  aws.String(tagString),
		Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("Failed to upload object: %v", err)
	}

	// Cleanup
	defer client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	// Overwrite and add more metadata
	err = OverwriteS3Object(context.Background(), client, bucket, key, func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		// Add additional metadata
		info.Metadata["processed"] = aws.String("true")
		info.Metadata["timestamp"] = aws.String(time.Now().Format(time.RFC3339))

		return srcFilePath, false, nil
	})

	if err != nil {
		t.Fatalf("Failed to overwrite: %v", err)
	}

	// Verify all tags preserved
	tagResp, err := client.GetObjectTagging(context.Background(), &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get tags: %v", err)
	}

	if len(tagResp.TagSet) != 10 {
		t.Errorf("Expected 10 tags, got %d", len(tagResp.TagSet))
	}

	// Verify metadata
	getResp, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get object: %v", err)
	}
	defer getResp.Body.Close()

	// Should have original 5 + 2 new metadata entries
	if len(getResp.Metadata) < 7 {
		t.Errorf("Expected at least 7 metadata entries, got %d", len(getResp.Metadata))
	}
}

func TestE2E_SpecificUserAndEmailGrantees(t *testing.T) {
	client := getTestS3Client(t)
	bucket := os.Getenv("TEST_BUCKET")
	key := fmt.Sprintf("go-s3-overwrite-test/%d/user-email-acl.txt", time.Now().Unix())

	// Get current user's canonical ID
	callerResp, err := client.GetBucketAcl(context.Background(), &s3.GetBucketAclInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		t.Fatalf("Failed to get bucket ACL for user ID: %v", err)
	}

	var ownerID string
	for _, grant := range callerResp.Grants {
		if string(grant.Permission) == "FULL_CONTROL" && grant.Grantee.ID != nil {
			ownerID = *grant.Grantee.ID
			break
		}
	}

	if ownerID == "" {
		t.Skip("Could not determine owner ID")
	}

	// Upload object
	_, err = client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        strings.NewReader("test content"),
		ContentType: aws.String("text/plain"),
		Metadata: map[string]string{
			"test": "user-email-grants",
		},
	})
	if err != nil {
		t.Fatalf("Failed to upload test object: %v", err)
	}

	// Set ACL with specific user ID grants
	// Note: Email-based grants may not work in all regions/accounts
	_, err = client.PutObjectAcl(context.Background(), &s3.PutObjectAclInput{
		Bucket:       aws.String(bucket),
		Key:          aws.String(key),
		GrantRead:    aws.String(fmt.Sprintf("id=\"%s\"", ownerID)),
		GrantReadACP: aws.String(fmt.Sprintf("id=\"%s\"", ownerID)),
	})
	if err != nil {
		// If email grants fail, just test with ID grants
		t.Logf("Setting ACL with email grants failed (expected in some environments): %v", err)
	}

	// Cleanup
	defer client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	// Test overwrite with ACL preservation
	err = OverwriteS3Object(context.Background(), client, bucket, key, func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		// Modify content
		modifiedFile, err := os.CreateTemp("", "e2e-grants-*.txt")
		if err != nil {
			return "", false, err
		}
		defer modifiedFile.Close()
		
		if _, err := modifiedFile.WriteString("modified content with user grants"); err != nil {
			os.Remove(modifiedFile.Name())
			return "", false, err
		}

		// Add metadata
		info.Metadata["modified"] = aws.String("true")

		return modifiedFile.Name(), true, nil
	})

	if err != nil {
		t.Fatalf("Failed to overwrite object: %v", err)
	}

	// Verify ACL is preserved
	aclResp, err := client.GetObjectAcl(context.Background(), &s3.GetObjectAclInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get ACL: %v", err)
	}

	// Check that specific user ID grants are preserved
	hasUserRead := false
	hasUserReadACP := false
	for _, grant := range aclResp.Grants {
		if grant.Grantee.ID != nil && *grant.Grantee.ID == ownerID {
			{
				switch string(grant.Permission) {
				case "READ":
					hasUserRead = true
				case "READ_ACP":
					hasUserReadACP = true
				}
			}
		}
	}

	if !hasUserRead && !hasUserReadACP {
		t.Error("User-specific grants were not preserved")
	}

	// Verify metadata
	getResp, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("Failed to get object: %v", err)
	}
	defer getResp.Body.Close()

	if val, ok := getResp.Metadata["Modified"]; !ok || val != "true" {
		t.Error("Metadata not updated correctly")
	}
}