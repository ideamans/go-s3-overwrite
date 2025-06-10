# go-s3-overwrite

[![日本語](https://img.shields.io/badge/lang-%E6%97%A5%E6%9C%AC%E8%AA%9E-blue.svg)](README.ja.md)

A simple Go package for overwriting S3 objects while preserving their metadata, tags, and ACLs.

## Overview

When you overwrite an S3 object using the standard PutObject operation, AWS S3 internally deletes and recreates the object, causing the loss of:

- Object tags
- ACL settings
- Custom metadata
- Attributes like ContentType and CacheControl

This package solves this problem by providing two simple functions that automatically preserve these attributes during object overwrites.

## Installation

```bash
go get github.com/ideamans/go-s3-overwrite
```

## Usage

### Basic Example: Preserve Existing ACL

```go
package main

import (
    "fmt"
    "log"
    
    "github.com/aws/aws-sdk-go/aws/session"
    "github.com/aws/aws-sdk-go/service/s3"
    overwrite "github.com/ideamans/go-s3-overwrite"
)

func main() {
    sess := session.Must(session.NewSession())
    svc := s3.New(sess)
    
    err := overwrite.OverwriteS3Object(svc, "my-bucket", "path/to/file.txt",
        func(info overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
            // Object metadata is available in info
            fmt.Printf("Processing: %s (size: %d bytes)\n", 
                info.Key, *info.ContentLength)
            
            // Read the file content
            content, err := os.ReadFile(srcFilePath)
            if err != nil {
                return "", false, err
            }
            
            // Process the content (example: convert to uppercase)
            modified := strings.ToUpper(string(content))
            
            // Create a new file with modified content
            modifiedFile, err := os.CreateTemp("", "modified-*.txt")
            if err != nil {
                return "", false, err
            }
            defer modifiedFile.Close()
            
            if _, err := modifiedFile.WriteString(modified); err != nil {
                os.Remove(modifiedFile.Name())
                return "", false, err
            }
            
            // Return the path to the modified file to overwrite, or "" to skip
            // autoRemove = true to automatically clean up the temporary file
            return modifiedFile.Name(), true, nil
        })
    
    if err != nil {
        log.Fatal(err)
    }
}
```

### Example: Set Simple ACL

```go
err := overwrite.OverwriteS3ObjectWithAcl(svc, "my-bucket", "public/image.jpg", "public-read",
    func(info overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
        // Skip files larger than 10MB
        if *info.ContentLength > 10*1024*1024 {
            return "", false, nil
        }
        
        // Process image optimization here...
        // For example, return the same file if no changes needed
        
        return srcFilePath, false, nil
    })
```

### Example: JSON Formatting

```go
err := overwrite.OverwriteS3Object(svc, "my-bucket", "data/config.json",
    func(info overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
        // Read JSON
        data, err := os.ReadFile(srcFilePath)
        if err != nil {
            return "", false, err
        }
        
        var jsonData interface{}
        if err := json.Unmarshal(data, &jsonData); err != nil {
            return "", false, fmt.Errorf("invalid JSON: %w", err)
        }
        
        // Format with indentation
        formatted, err := json.MarshalIndent(jsonData, "", "  ")
        if err != nil {
            return "", false, err
        }
        
        // Add metadata
        if info.Metadata == nil {
            info.Metadata = make(map[string]*string)
        }
        info.Metadata["formatted"] = aws.String("true")
        info.Metadata["formatted-at"] = aws.String(time.Now().Format(time.RFC3339))
        
        // Create new file with formatted JSON
        formattedFile, err := os.CreateTemp("", "formatted-*.json")
        if err != nil {
            return "", false, err
        }
        defer formattedFile.Close()
        
        if _, err := formattedFile.Write(formatted); err != nil {
            os.Remove(formattedFile.Name())
            return "", false, err
        }
        
        return formattedFile.Name(), true, nil
    })
```

## API Reference

### Functions

#### OverwriteS3Object

Overwrites an S3 object while preserving its existing ACL.

```go
func OverwriteS3Object(
    client S3Client,
    bucket string,
    key string,
    callback OverwriteCallback,
) error
```

**Parameters:**
- `client`: AWS S3 client that implements the S3Client interface
- `bucket`: S3 bucket name
- `key`: S3 object key
- `callback`: Function to process the object

#### OverwriteS3ObjectWithAcl

Overwrites an S3 object with a specific simple ACL.

```go
func OverwriteS3ObjectWithAcl(
    client S3Client,
    bucket string,
    key string,
    acl string,
    callback OverwriteCallback,
) error
```

**Parameters:**
- `client`: AWS S3 client that implements the S3Client interface
- `bucket`: S3 bucket name
- `key`: S3 object key
- `acl`: Simple ACL to apply (`"private"`, `"public-read"`, `"public-read-write"`, `"authenticated-read"`)
- `callback`: Function to process the object

### Types

#### ObjectInfo

Contains S3 object metadata passed to the callback function.

```go
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
```

#### OverwriteCallback

Callback function signature for processing objects.

```go
type OverwriteCallback func(info ObjectInfo, srcFilePath string) (overwritingFilePath string, autoRemove bool, err error)
```

**Parameters:**
- `info`: Object metadata
- `srcFilePath`: Path to the temporary file containing the object's content

**Returns:**
- `overwritingFilePath`: Path to the file to upload (return empty string "" to skip overwrite)
- `autoRemove`: If true, the file at `overwritingFilePath` will be automatically removed after upload (only if different from `srcFilePath`)
- `err`: Any error that occurred during processing

### S3Client Interface

The minimal interface required for S3 operations. The AWS SDK's `*s3.S3` type implements this interface.

```go
type S3Client interface {
    GetObject(input *s3.GetObjectInput) (*s3.GetObjectOutput, error)
    GetObjectTagging(input *s3.GetObjectTaggingInput) (*s3.GetObjectTaggingOutput, error)
    GetObjectAcl(input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error)
    PutObject(input *s3.PutObjectInput) (*s3.PutObjectOutput, error)
    PutObjectAcl(input *s3.PutObjectAclInput) (*s3.PutObjectAclOutput, error)
}
```

## How It Works

1. Downloads the object to a temporary file
2. Builds ObjectInfo struct from object metadata
3. Calls your callback function with the metadata and temp file path
4. If callback returns a non-empty file path:
   - Fetches existing tags and ACL
   - Uploads the file content from the returned path with preserved attributes
   - Restores WRITE permissions if needed (via PutObjectAcl)
5. Always cleans up the temporary file

## Required IAM Permissions

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:GetObjectTagging",
        "s3:GetObjectAcl",
        "s3:PutObject",
        "s3:PutObjectAcl"
      ],
      "Resource": "arn:aws:s3:::your-bucket/*"
    }
  ]
}
```

## Testing

### Unit Tests

```bash
# Run unit tests
go test -v ./...

# Run tests with coverage
go test -v -race -cover ./...
```

### End-to-End Tests

The package includes comprehensive E2E tests that verify functionality against real S3 buckets.

#### Prerequisites

1. Create a test S3 bucket
2. Enable public access and ACLs on the bucket (required for ACL preservation tests)
3. Set up AWS credentials (via AWS profile or access keys)

#### Running E2E Tests

```bash
# Using AWS profile (recommended)
TEST_BUCKET=your-test-bucket AWS_PROFILE=your-profile go test -v -tags=e2e ./...

# Using AWS access keys
TEST_BUCKET=your-test-bucket \
  AWS_ACCESS_KEY_ID=your-key \
  AWS_SECRET_ACCESS_KEY=your-secret \
  AWS_REGION=us-east-1 \
  go test -v -tags=e2e ./...

# Create a .env file for convenience
echo "TEST_BUCKET=your-test-bucket" > .env
echo "AWS_PROFILE=your-profile" >> .env
go test -v -tags=e2e ./...
```

#### E2E Test Coverage

The E2E tests verify:

- ACL preservation (simple and complex ACLs)
- Metadata preservation with special characters
- Tag preservation with URL encoding
- Content-Type and Cache-Control attributes
- Multiple grantee types (ID, URI, email)
- Error handling and edge cases
- Temporary file cleanup

## Contributing

Pull requests are welcome. For major changes, please open an issue first to discuss what you would like to change.

Please make sure to update tests as appropriate.

## License

[MIT](LICENSE)