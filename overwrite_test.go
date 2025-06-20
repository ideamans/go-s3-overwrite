package overwrite

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// mockS3Client is a mock implementation of S3Client for testing
type mockS3Client struct {
	getObjectFunc        func(context.Context, *s3.GetObjectInput) (*s3.GetObjectOutput, error)
	getObjectTaggingFunc func(context.Context, *s3.GetObjectTaggingInput) (*s3.GetObjectTaggingOutput, error)
	getObjectAclFunc     func(context.Context, *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error)
	putObjectFunc        func(context.Context, *s3.PutObjectInput) (*s3.PutObjectOutput, error)
	putObjectAclFunc     func(context.Context, *s3.PutObjectAclInput) (*s3.PutObjectAclOutput, error)
}

func (m *mockS3Client) GetObject(ctx context.Context, input *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.getObjectFunc != nil {
		return m.getObjectFunc(ctx, input)
	}
	return nil, errors.New("not implemented")
}

func (m *mockS3Client) GetObjectTagging(ctx context.Context, input *s3.GetObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.GetObjectTaggingOutput, error) {
	if m.getObjectTaggingFunc != nil {
		return m.getObjectTaggingFunc(ctx, input)
	}
	return nil, errors.New("not implemented")
}

func (m *mockS3Client) GetObjectAcl(ctx context.Context, input *s3.GetObjectAclInput, optFns ...func(*s3.Options)) (*s3.GetObjectAclOutput, error) {
	if m.getObjectAclFunc != nil {
		return m.getObjectAclFunc(ctx, input)
	}
	return nil, errors.New("not implemented")
}

func (m *mockS3Client) PutObject(ctx context.Context, input *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if m.putObjectFunc != nil {
		return m.putObjectFunc(ctx, input)
	}
	return nil, errors.New("not implemented")
}

func (m *mockS3Client) PutObjectAcl(ctx context.Context, input *s3.PutObjectAclInput, optFns ...func(*s3.Options)) (*s3.PutObjectAclOutput, error) {
	if m.putObjectAclFunc != nil {
		return m.putObjectAclFunc(ctx, input)
	}
	return nil, errors.New("not implemented")
}

// Test OverwriteS3Object with successful overwrite
func TestOverwriteS3Object_Success(t *testing.T) {
	content := "test content"
	tagCount := int64(2)
	contentType := "text/plain"
	lastModified := time.Now()

	client := &mockS3Client{
		getObjectFunc: func(ctx context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body:         io.NopCloser(strings.NewReader(content)),
				ContentType:  aws.String(contentType),
				TagCount:     aws.Int32(int32(tagCount)),
				LastModified: aws.Time(lastModified),
				Metadata: map[string]string{
					"key1": "value1",
				},
			}, nil
		},
		getObjectTaggingFunc: func(ctx context.Context, input *s3.GetObjectTaggingInput) (*s3.GetObjectTaggingOutput, error) {
			return &s3.GetObjectTaggingOutput{
				TagSet: []types.Tag{
					{Key: aws.String("tag1"), Value: aws.String("value1")},
					{Key: aws.String("tag2"), Value: aws.String("value2")},
				},
			}, nil
		},
		getObjectAclFunc: func(ctx context.Context, input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error) {
			return &s3.GetObjectAclOutput{
				Grants: []types.Grant{
					{
						Grantee: &types.Grantee{
							Type: types.TypeCanonicalUser,
							ID:   aws.String("123456"),
						},
						Permission: types.PermissionRead,
					},
				},
			}, nil
		},
		putObjectFunc: func(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			// Verify tags are preserved
			if input.Tagging == nil || *input.Tagging != "tag1=value1&tag2=value2" {
				t.Errorf("Expected tagging 'tag1=value1&tag2=value2', got %v", input.Tagging)
			}
			// Verify grant is added
			if input.GrantRead == nil || *input.GrantRead != `id="123456"` {
				t.Errorf("Expected grant read 'id=\"123456\"', got %v", input.GrantRead)
			}
			return &s3.PutObjectOutput{}, nil
		},
	}

	callbackCalled := false
	err := OverwriteS3Object(context.Background(), client, "test-bucket", "test-key", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		callbackCalled = true

		// Verify ObjectInfo
		if info.Bucket != "test-bucket" {
			t.Errorf("Expected bucket 'test-bucket', got %s", info.Bucket)
		}
		if info.Key != "test-key" {
			t.Errorf("Expected key 'test-key', got %s", info.Key)
		}
		if aws.ToString(info.ContentType) != contentType {
			t.Errorf("Expected content type %s, got %s", contentType, aws.ToString(info.ContentType))
		}

		// Verify file content
		data, err := os.ReadFile(srcFilePath)
		if err != nil {
			return "", false, err
		}
		if string(data) != content {
			t.Errorf("Expected content '%s', got '%s'", content, string(data))
		}

		// Create a new file with modified content
		modifiedFile, err := os.CreateTemp("", "modified-*.tmp")
		if err != nil {
			return "", false, err
		}
		defer modifiedFile.Close()

		if _, err := modifiedFile.WriteString("modified content"); err != nil {
			os.Remove(modifiedFile.Name())
			return "", false, err
		}

		return modifiedFile.Name(), true, nil
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !callbackCalled {
		t.Error("Callback was not called")
	}
}

// Test OverwriteS3Object when callback returns false (skip)
func TestOverwriteS3Object_Skip(t *testing.T) {
	putObjectCalled := false

	client := &mockS3Client{
		getObjectFunc: func(ctx context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(strings.NewReader("test content")),
			}, nil
		},
		putObjectFunc: func(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			putObjectCalled = true
			return &s3.PutObjectOutput{}, nil
		},
	}

	err := OverwriteS3Object(context.Background(), client, "test-bucket", "test-key", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		// Return empty string to skip overwrite
		return "", false, nil
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if putObjectCalled {
		t.Error("PutObject should not be called when callback returns false")
	}
}

// Test OverwriteS3Object with WRITE permission restoration
func TestOverwriteS3Object_WithWritePermission(t *testing.T) {
	putObjectAclCalled := false

	client := &mockS3Client{
		getObjectFunc: func(ctx context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(strings.NewReader("test content")),
			}, nil
		},
		getObjectAclFunc: func(ctx context.Context, input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error) {
			return &s3.GetObjectAclOutput{
				Grants: []types.Grant{
					{
						Grantee: &types.Grantee{
							Type: types.TypeGroup,
							URI:  aws.String("http://acs.amazonaws.com/groups/global/AllUsers"),
						},
						Permission: types.PermissionWrite,
					},
				},
			}, nil
		},
		putObjectFunc: func(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			// WRITE permission should not be in PutObject
			// PutObjectInput doesn't have GrantWrite field - it's only in PutObjectAclInput
			return &s3.PutObjectOutput{}, nil
		},
		putObjectAclFunc: func(ctx context.Context, input *s3.PutObjectAclInput) (*s3.PutObjectAclOutput, error) {
			putObjectAclCalled = true
			// Verify WRITE permission is restored
			if input.GrantWrite == nil || *input.GrantWrite != `uri="http://acs.amazonaws.com/groups/global/AllUsers"` {
				t.Errorf("Expected grant write, got %v", input.GrantWrite)
			}
			return &s3.PutObjectAclOutput{}, nil
		},
	}

	err := OverwriteS3Object(context.Background(), client, "test-bucket", "test-key", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		// Return the same file to overwrite
		return srcFilePath, false, nil
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !putObjectAclCalled {
		t.Error("PutObjectAcl should be called for WRITE permission restoration")
	}
}

// Test OverwriteS3Object error handling
func TestOverwriteS3Object_Errors(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*mockS3Client)
		expectedError string
	}{
		{
			name: "GetObject error",
			setupMock: func(m *mockS3Client) {
				m.getObjectFunc = func(ctx context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					return nil, errors.New("get object failed")
				}
			},
			expectedError: "failed to get object: get object failed",
		},
		{
			name: "Callback error",
			setupMock: func(m *mockS3Client) {
				m.getObjectFunc = func(ctx context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					return &s3.GetObjectOutput{
						Body: io.NopCloser(strings.NewReader("test")),
					}, nil
				}
			},
			expectedError: "callback error: callback failed",
		},
		{
			name: "GetObjectTagging error",
			setupMock: func(m *mockS3Client) {
				m.getObjectFunc = func(ctx context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					return &s3.GetObjectOutput{
						Body:     io.NopCloser(strings.NewReader("test")),
						TagCount: aws.Int32(1),
					}, nil
				}
				m.getObjectTaggingFunc = func(ctx context.Context, input *s3.GetObjectTaggingInput) (*s3.GetObjectTaggingOutput, error) {
					return nil, errors.New("tagging failed")
				}
			},
			expectedError: "failed to get object tagging: tagging failed",
		},
		{
			name: "GetObjectAcl error",
			setupMock: func(m *mockS3Client) {
				m.getObjectFunc = func(ctx context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					return &s3.GetObjectOutput{
						Body: io.NopCloser(strings.NewReader("test")),
					}, nil
				}
				m.getObjectAclFunc = func(ctx context.Context, input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error) {
					return nil, errors.New("acl failed")
				}
			},
			expectedError: "failed to get object ACL: acl failed",
		},
		{
			name: "PutObject error",
			setupMock: func(m *mockS3Client) {
				m.getObjectFunc = func(ctx context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					return &s3.GetObjectOutput{
						Body: io.NopCloser(strings.NewReader("test")),
					}, nil
				}
				m.getObjectAclFunc = func(ctx context.Context, input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error) {
					return &s3.GetObjectAclOutput{}, nil
				}
				m.putObjectFunc = func(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
					return nil, errors.New("put failed")
				}
			},
			expectedError: "failed to put object: put failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &mockS3Client{}
			tt.setupMock(client)

			err := OverwriteS3Object(context.Background(), client, "bucket", "key", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
				if tt.name == "Callback error" {
					return "", false, errors.New("callback failed")
				}
				return srcFilePath, false, nil
			})

			if err == nil {
				t.Error("Expected error but got none")
			} else if err.Error() != tt.expectedError {
				t.Errorf("Expected error '%s', got '%s'", tt.expectedError, err.Error())
			}
		})
	}
}

// Test OverwriteS3ObjectWithAcl with various ACL types
func TestOverwriteS3ObjectWithAcl_Success(t *testing.T) {
	aclTypes := []string{"private", "public-read", "public-read-write", "authenticated-read"}

	for _, acl := range aclTypes {
		t.Run(acl, func(t *testing.T) {
			client := &mockS3Client{
				getObjectFunc: func(ctx context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					return &s3.GetObjectOutput{
						Body:        io.NopCloser(strings.NewReader("test content")),
						ContentType: aws.String("text/plain"),
						TagCount:    aws.Int32(1),
					}, nil
				},
				getObjectTaggingFunc: func(ctx context.Context, input *s3.GetObjectTaggingInput) (*s3.GetObjectTaggingOutput, error) {
					return &s3.GetObjectTaggingOutput{
						TagSet: []types.Tag{
							{Key: aws.String("tag1"), Value: aws.String("value1")},
						},
					}, nil
				},
				putObjectFunc: func(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
					// Verify ACL is set
					if string(input.ACL) != acl {
						t.Errorf("Expected ACL '%s', got %v", acl, input.ACL)
					}
					// Verify content type is preserved
					if input.ContentType == nil || *input.ContentType != "text/plain" {
						t.Errorf("Content type not preserved")
					}
					// Verify tags are preserved
					if input.Tagging == nil || *input.Tagging != "tag1=value1" {
						t.Errorf("Tags not preserved")
					}
					return &s3.PutObjectOutput{}, nil
				},
			}

			err := OverwriteS3ObjectWithAcl(context.Background(), client, "test-bucket", "test-key", acl, func(info ObjectInfo, srcFilePath string) (string, bool, error) {
				// Create a new file with modified content
				modifiedFile, err := os.CreateTemp("", "modified-*.tmp")
				if err != nil {
					return "", false, err
				}
				defer modifiedFile.Close()

				if _, err := modifiedFile.WriteString("modified content"); err != nil {
					os.Remove(modifiedFile.Name())
					return "", false, err
				}

				return modifiedFile.Name(), true, nil
			})

			if err != nil {
				t.Fatalf("Unexpected error for ACL %s: %v", acl, err)
			}
		})
	}
}

// Test OverwriteS3ObjectWithAcl when callback returns false (skip)
func TestOverwriteS3ObjectWithAcl_Skip(t *testing.T) {
	putObjectCalled := false

	client := &mockS3Client{
		getObjectFunc: func(ctx context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(strings.NewReader("test content")),
			}, nil
		},
		putObjectFunc: func(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			putObjectCalled = true
			return &s3.PutObjectOutput{}, nil
		},
	}

	err := OverwriteS3ObjectWithAcl(context.Background(), client, "test-bucket", "test-key", "private", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		// Return empty string to skip overwrite
		return "", false, nil
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if putObjectCalled {
		t.Error("PutObject should not be called when callback returns false")
	}
}

// Test autoRemove functionality
func TestAutoRemove(t *testing.T) {
	var createdFilePath string

	client := &mockS3Client{
		getObjectFunc: func(ctx context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(strings.NewReader("test content")),
			}, nil
		},
		getObjectAclFunc: func(ctx context.Context, input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error) {
			return &s3.GetObjectAclOutput{}, nil
		},
		putObjectFunc: func(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			return &s3.PutObjectOutput{}, nil
		},
	}

	// Test with autoRemove = true
	err := OverwriteS3Object(context.Background(), client, "test-bucket", "test-key", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		// Create a new temporary file
		tmpFile, err := os.CreateTemp("", "autoremove-test-*.tmp")
		if err != nil {
			return "", false, err
		}
		tmpFile.Close()
		createdFilePath = tmpFile.Name()
		
		// Write some content
		if err := os.WriteFile(createdFilePath, []byte("new content"), 0600); err != nil {
			os.Remove(createdFilePath)
			return "", false, err
		}
		
		return createdFilePath, true, nil // autoRemove = true
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check that the file was automatically removed
	if _, err := os.Stat(createdFilePath); !os.IsNotExist(err) {
		t.Error("File with autoRemove=true was not automatically cleaned up")
		os.Remove(createdFilePath) // Clean up manually if test fails
	}

	// Test with autoRemove = false
	err = OverwriteS3Object(context.Background(), client, "test-bucket", "test-key", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		// Create a new temporary file
		tmpFile, err := os.CreateTemp("", "no-autoremove-test-*.tmp")
		if err != nil {
			return "", false, err
		}
		tmpFile.Close()
		createdFilePath = tmpFile.Name()
		
		// Write some content
		if err := os.WriteFile(createdFilePath, []byte("new content"), 0600); err != nil {
			os.Remove(createdFilePath)
			return "", false, err
		}
		
		return createdFilePath, false, nil // autoRemove = false
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check that the file still exists
	if _, err := os.Stat(createdFilePath); os.IsNotExist(err) {
		t.Error("File with autoRemove=false was incorrectly removed")
	} else {
		// Clean up manually
		os.Remove(createdFilePath)
	}
}

// Test autoRemove edge cases
func TestAutoRemoveEdgeCases(t *testing.T) {
	client := &mockS3Client{
		getObjectFunc: func(ctx context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(strings.NewReader("test content")),
			}, nil
		},
		getObjectAclFunc: func(ctx context.Context, input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error) {
			return &s3.GetObjectAclOutput{}, nil
		},
		putObjectFunc: func(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			return &s3.PutObjectOutput{}, nil
		},
	}

	t.Run("autoRemove with same srcFilePath", func(t *testing.T) {
		// Test that autoRemove=true does NOT remove the file when it's the same as srcFilePath
		// This test verifies the logic, not the actual file existence (since defer will clean it up)
		autoRemoveCalled := false
		originalPath := ""
		
		// Create a custom client that tracks if the file would be removed
		testClient := &mockS3Client{
			getObjectFunc: client.getObjectFunc,
			getObjectAclFunc: client.getObjectAclFunc,
			putObjectFunc: func(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
				// At this point, check if the file still exists
				if originalPath != "" {
					if _, err := os.Stat(originalPath); os.IsNotExist(err) {
						autoRemoveCalled = true
					}
				}
				return &s3.PutObjectOutput{}, nil
			},
		}
		
		err := OverwriteS3Object(context.Background(), testClient, "test-bucket", "test-key", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
			originalPath = srcFilePath
			// Return the same file path with autoRemove=true
			return srcFilePath, true, nil
		})

		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		// The file should NOT have been removed by autoRemove logic
		if autoRemoveCalled {
			t.Error("autoRemove logic incorrectly tried to remove srcFilePath when it was the same as overwritingFilePath")
		}
	})

	t.Run("autoRemove with empty path", func(t *testing.T) {
		// Test that autoRemove=true with empty path doesn't cause issues
		err := OverwriteS3Object(context.Background(), client, "test-bucket", "test-key", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
			// Return empty path (skip) with autoRemove=true
			return "", true, nil
		})

		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
	})

	t.Run("autoRemove in OverwriteS3ObjectWithAcl", func(t *testing.T) {
		// Test autoRemove functionality in OverwriteS3ObjectWithAcl
		var createdFilePath string
		err := OverwriteS3ObjectWithAcl(context.Background(), client, "test-bucket", "test-key", "private", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
			// Create a new temporary file
			tmpFile, err := os.CreateTemp("", "acl-autoremove-test-*.tmp")
			if err != nil {
				return "", false, err
			}
			tmpFile.Close()
			createdFilePath = tmpFile.Name()
			
			// Write some content
			if err := os.WriteFile(createdFilePath, []byte("new content"), 0600); err != nil {
				os.Remove(createdFilePath)
				return "", false, err
			}
			
			return createdFilePath, true, nil // autoRemove = true
		})

		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		// Check that the file was automatically removed
		if _, err := os.Stat(createdFilePath); !os.IsNotExist(err) {
			t.Error("File with autoRemove=true was not automatically cleaned up in OverwriteS3ObjectWithAcl")
			os.Remove(createdFilePath) // Clean up manually if test fails
		}
	})

	t.Run("autoRemove cleanup happens after upload", func(t *testing.T) {
		// Test that file is removed AFTER successful upload, not before
		var fileExistsDuringUpload bool
		createdFilePath := ""
		
		testClient := &mockS3Client{
			getObjectFunc: client.getObjectFunc,
			getObjectAclFunc: client.getObjectAclFunc,
			putObjectFunc: func(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
				// Check if the file exists during upload
				if createdFilePath != "" {
					if _, err := os.Stat(createdFilePath); err == nil {
						fileExistsDuringUpload = true
					}
				}
				return &s3.PutObjectOutput{}, nil
			},
		}
		
		err := OverwriteS3Object(context.Background(), testClient, "test-bucket", "test-key", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
			// Create a new temporary file
			tmpFile, err := os.CreateTemp("", "upload-timing-test-*.tmp")
			if err != nil {
				return "", false, err
			}
			tmpFile.Close()
			createdFilePath = tmpFile.Name()
			
			// Write some content
			if err := os.WriteFile(createdFilePath, []byte("test content"), 0600); err != nil {
				os.Remove(createdFilePath)
				return "", false, err
			}
			
			return createdFilePath, true, nil // autoRemove = true
		})

		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if !fileExistsDuringUpload {
			t.Error("File was removed before upload completed")
		}
		
		// File should be removed after upload
		if _, err := os.Stat(createdFilePath); !os.IsNotExist(err) {
			t.Error("File was not removed after upload")
			os.Remove(createdFilePath)
		}
	})
}

// Test temporary file cleanup
func TestTemporaryFileCleanup(t *testing.T) {
	var tmpFileName string

	client := &mockS3Client{
		getObjectFunc: func(ctx context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(strings.NewReader("test content")),
			}, nil
		},
		getObjectAclFunc: func(ctx context.Context, input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error) {
			return &s3.GetObjectAclOutput{}, nil
		},
		putObjectFunc: func(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			return &s3.PutObjectOutput{}, nil
		},
	}

	// Test successful case - temp file should be cleaned up
	err := OverwriteS3Object(context.Background(), client, "test-bucket", "test-key", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		tmpFileName = srcFilePath
		return srcFilePath, false, nil
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check temp file is deleted
	if _, err := os.Stat(tmpFileName); !os.IsNotExist(err) {
		t.Error("Temporary file was not cleaned up")
	}

	// Test error case - temp file should still be cleaned up
	err = OverwriteS3Object(context.Background(), client, "test-bucket", "test-key", func(info ObjectInfo, srcFilePath string) (string, bool, error) {
		tmpFileName = srcFilePath
		return "", false, errors.New("intentional error")
	})

	if err == nil {
		t.Error("Expected error but got none")
	}

	// Check temp file is deleted even on error
	if _, err := os.Stat(tmpFileName); !os.IsNotExist(err) {
		t.Error("Temporary file was not cleaned up after error")
	}
}


// Test buildTaggingString
func TestBuildTaggingString(t *testing.T) {
	tests := []struct {
		name     string
		tags     []types.Tag
		expected string
	}{
		{
			name:     "Empty tags",
			tags:     []types.Tag{},
			expected: "",
		},
		{
			name: "Single tag",
			tags: []types.Tag{
				{Key: aws.String("key1"), Value: aws.String("value1")},
			},
			expected: "key1=value1",
		},
		{
			name: "Multiple tags",
			tags: []types.Tag{
				{Key: aws.String("key1"), Value: aws.String("value1")},
				{Key: aws.String("key2"), Value: aws.String("value2")},
			},
			expected: "key1=value1&key2=value2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildTaggingString(tt.tags)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

// Test buildGrantString
func TestBuildGrantString(t *testing.T) {
	grants := []types.Grant{
		{
			Grantee: &types.Grantee{
				Type: types.TypeCanonicalUser,
				ID:   aws.String("123456"),
			},
			Permission: types.PermissionRead,
		},
		{
			Grantee: &types.Grantee{
				Type: types.TypeGroup,
				URI:  aws.String("http://acs.amazonaws.com/groups/global/AllUsers"),
			},
			Permission: types.PermissionRead,
		},
		{
			Grantee: &types.Grantee{
				Type:         types.TypeAmazonCustomerByEmail,
				EmailAddress: aws.String("test@example.com"),
			},
			Permission: types.PermissionWrite,
		},
	}

	tests := []struct {
		permission string
		expected   string
	}{
		{"READ", `id="123456",uri="http://acs.amazonaws.com/groups/global/AllUsers"`}, // Multiple READ grants
		{"WRITE", `emailaddress="test@example.com"`},
		{"FULL_CONTROL", ""}, // No matching grant
	}

	for _, tt := range tests {
		t.Run(tt.permission, func(t *testing.T) {
			result := buildGrantString(grants, tt.permission)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}