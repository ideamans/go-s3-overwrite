# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Design Documentation

The detailed design specification for this package is available in `DESIGN.md`. It contains comprehensive information about:
- Package overview and problem statement
- API specifications and data structures
- Implementation details and processing flow
- Testing strategy and CI/CD configuration
- Required IAM permissions

## Common Development Tasks

### Building and Testing

```bash
# Run unit tests
go test -v ./...

# Run unit tests with race detection and coverage
go test -v -race -cover ./...

# Run E2E tests (requires TEST_BUCKET environment variable)
TEST_BUCKET=your-test-bucket go test -v -tags=e2e ./...

# Install dependencies
go mod download

# Tidy dependencies
go mod tidy
```

### Environment Setup

Copy `.env.example` to `.env` and configure:

```bash
TEST_BUCKET=your-test-bucket-name
AWS_ACCESS_KEY_ID=your-access-key
AWS_SECRET_ACCESS_KEY=your-secret-key
AWS_REGION=us-east-1
```

## Architecture Overview

This package provides a simple callback-based API for overwriting S3 objects while preserving their metadata, tags, and ACLs. The design follows these principles:

1. **Two main functions**: `OverwriteS3Object` (preserves existing ACL) and `OverwriteS3ObjectWithAcl` (sets simple ACL)
2. **Callback pattern**: User provides a function that receives object info and a temporary file, returns whether to overwrite
3. **Automatic preservation**: Tags, metadata, and object attributes are automatically preserved during overwrite
4. **Clean error handling**: All errors are wrapped with context using fmt.Errorf with %w verb

### Key Components

- **ObjectInfo struct**: Contains S3 object metadata passed to callbacks
- **OverwriteCallback**: Function signature for processing objects
- **Temporary file handling**: Downloads to temp file with 0600 permissions, always cleaned up

### Processing Flow

1. Download object to temporary file
2. Build ObjectInfo from object metadata  
3. Call user's callback function
4. If callback returns true:
   - Fetch existing tags and ACL
   - Upload modified content with preserved attributes
   - Restore WRITE permissions if needed (via PutObjectAcl)
5. Clean up temporary file

### Reference Implementation

The `reference/` directory contains the original `s3plus` package that inspired this design. Key differences:
- Simplified API (2 functions vs multiple)
- Callback-based instead of direct function calls
- No dependency on custom localization (`go-l10n`)
- Cleaner interface design for testability

## Testing Strategy

### Unit Tests

- Mock S3 client interfaces for dependency-free testing
- Test cases cover normal operation, errors, callbacks, and cleanup

### Integration Tests

- Run only when `TEST_BUCKET` is set
- Test against real S3 to verify ACL/tag preservation
- Required IAM permissions: GetObject, GetObjectTagging, GetObjectAcl, PutObject, PutObjectAcl
