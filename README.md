# go-s3-overwrite

[![日本語](https://img.shields.io/badge/lang-%E6%97%A5%E6%9C%AC%E8%AA%9E-blue.svg)](README.ja.md)

A simple Go package for overwriting S3 objects while preserving their metadata, tags, and ACLs.

## Version 2.0 Breaking Changes

**If you're upgrading from v1.x to v2.0**, please see the [Migration Guide](#migration-from-v1x-to-v20) below.

## Overview

When you overwrite an S3 object using the standard PutObject operation, AWS S3 internally deletes and recreates the object, causing the loss of:

- Object tags
- ACL settings
- Custom metadata
- Attributes like ContentType and CacheControl

This package solves this problem by providing two simple functions that automatically preserve these attributes during object overwrites.

## Installation

```bash
go get github.com/ideamans/go-s3-overwrite/v2
```

## Usage

### Basic Example: Preserve Existing ACL

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "strings"

    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    overwrite "github.com/ideamans/go-s3-overwrite/v2"
)

func main() {
    // Load AWS configuration
    cfg, err := config.LoadDefaultConfig(context.TODO())
    if err != nil {
        log.Fatal(err)
    }
    svc := s3.NewFromConfig(cfg)
    
    err = overwrite.OverwriteS3Object(context.Background(), svc, "my-bucket", "path/to/file.txt",
        func(info *overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
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
err := overwrite.OverwriteS3ObjectWithAcl(context.Background(), svc, "my-bucket", "public/image.jpg", "public-read",
    func(info *overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
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
import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "time"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    overwrite "github.com/ideamans/go-s3-overwrite/v2"
)

// Load AWS configuration
cfg, err := config.LoadDefaultConfig(context.TODO())
if err != nil {
    log.Fatal(err)
}
svc := s3.NewFromConfig(cfg)

err = overwrite.OverwriteS3Object(context.Background(), svc, "my-bucket", "data/config.json",
    func(info *overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
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
    ctx context.Context,
    client S3Client,
    bucket string,
    key string,
    callback OverwriteCallback,
) error
```

**Parameters:**
- `ctx`: Context for the operation
- `client`: AWS S3 client that implements the S3Client interface
- `bucket`: S3 bucket name
- `key`: S3 object key
- `callback`: Function to process the object

#### OverwriteS3ObjectWithAcl

Overwrites an S3 object with a specific simple ACL.

```go
func OverwriteS3ObjectWithAcl(
    ctx context.Context,
    client S3Client,
    bucket string,
    key string,
    acl string,
    callback OverwriteCallback,
) error
```

**Parameters:**
- `ctx`: Context for the operation
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
type OverwriteCallback func(info *ObjectInfo, srcFilePath string) (overwritingFilePath string, autoRemove bool, err error)
```

**Parameters:**
- `info`: Pointer to object metadata. **You can modify fields in ObjectInfo** (e.g., add/update metadata, change ContentType) and the changes will be applied to the uploaded object.
- `srcFilePath`: Path to the temporary file containing the object's content

**Returns:**
- `overwritingFilePath`: Path to the file to upload (return empty string "" to skip overwrite)
- `autoRemove`: If true, the file at `overwritingFilePath` will be automatically removed after upload (only if different from `srcFilePath`)
- `err`: Any error that occurred during processing

**Note:** In v2.0+, `info` is a pointer, allowing you to modify metadata and other fields within the callback. These modifications will persist when the object is uploaded.

### S3Client Interface

The minimal interface required for S3 operations. The AWS SDK v2's `*s3.Client` type implements this interface.

```go
type S3Client interface {
    GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
    GetObjectTagging(ctx context.Context, params *s3.GetObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.GetObjectTaggingOutput, error)
    GetObjectAcl(ctx context.Context, params *s3.GetObjectAclInput, optFns ...func(*s3.Options)) (*s3.GetObjectAclOutput, error)
    PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
    PutObjectAcl(ctx context.Context, params *s3.PutObjectAclInput, optFns ...func(*s3.Options)) (*s3.PutObjectAclOutput, error)
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

## Migration from v1.x to v2.0

Version 2.0 introduces a breaking change to enable metadata modification within callbacks.

### What Changed

The `OverwriteCallback` function signature now receives a **pointer** to `ObjectInfo` instead of a value:

**v1.x:**
```go
func(info overwrite.ObjectInfo, srcFilePath string) (string, bool, error)
```

**v2.0:**
```go
func(info *overwrite.ObjectInfo, srcFilePath string) (string, bool, error)
```

### Why This Change?

In v1.x, the callback received `ObjectInfo` by value, meaning any modifications to `info.Metadata` or other fields were lost. The documentation incorrectly suggested that metadata modifications would work.

In v2.0, `info` is a pointer, allowing you to:
- Add or modify metadata entries
- Change `ContentType` or other object attributes
- Have these changes applied when the object is uploaded

### How to Migrate

**Simply add `*` to your callback signatures:**

```diff
- func(info overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
+ func(info *overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
      // Your code remains the same
      return srcFilePath, false, nil
  }
```

### New Capabilities in v2.0

Now you can modify object metadata within callbacks:

```go
func(info *overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
    // Add processing metadata
    if info.Metadata == nil {
        info.Metadata = make(map[string]*string)
    }
    info.Metadata["processed"] = aws.String("true")
    info.Metadata["processed-at"] = aws.String(time.Now().Format(time.RFC3339))

    // Change content type
    info.ContentType = aws.String("application/json")

    return modifiedFilePath, true, nil
}
```

### Additional Examples

**Mark object as processed:**
```go
callback := func(info *overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
    if info.Metadata == nil {
        info.Metadata = make(map[string]*string)
    }
    info.Metadata["status"] = aws.String("processed")
    return processedFilePath, false, nil
}
```

**Fix incorrect content type:**
```go
callback := func(info *overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
    // Correct content type for JSON files
    info.ContentType = aws.String("application/json; charset=utf-8")
    return srcFilePath, false, nil
}
```

**Add version tracking:**
```go
callback := func(info *overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
    if info.Metadata == nil {
        info.Metadata = make(map[string]*string)
    }
    info.Metadata["version"] = aws.String("2.0")
    info.Metadata["updated-by"] = aws.String("image-processor")
    return newFilePath, false, nil
}
```

## Contributing

Pull requests are welcome. For major changes, please open an issue first to discuss what you would like to change.

Please make sure to update tests as appropriate.

## License

[MIT](LICENSE)