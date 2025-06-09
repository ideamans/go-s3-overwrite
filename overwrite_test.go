package overwrite

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
)

// mockS3Client is a mock implementation of S3Client for testing
type mockS3Client struct {
	getObjectFunc        func(*s3.GetObjectInput) (*s3.GetObjectOutput, error)
	getObjectTaggingFunc func(*s3.GetObjectTaggingInput) (*s3.GetObjectTaggingOutput, error)
	getObjectAclFunc     func(*s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error)
	putObjectFunc        func(*s3.PutObjectInput) (*s3.PutObjectOutput, error)
	putObjectAclFunc     func(*s3.PutObjectAclInput) (*s3.PutObjectAclOutput, error)
}

func (m *mockS3Client) GetObject(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
	if m.getObjectFunc != nil {
		return m.getObjectFunc(input)
	}
	return nil, errors.New("not implemented")
}

func (m *mockS3Client) GetObjectTagging(input *s3.GetObjectTaggingInput) (*s3.GetObjectTaggingOutput, error) {
	if m.getObjectTaggingFunc != nil {
		return m.getObjectTaggingFunc(input)
	}
	return nil, errors.New("not implemented")
}

func (m *mockS3Client) GetObjectAcl(input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error) {
	if m.getObjectAclFunc != nil {
		return m.getObjectAclFunc(input)
	}
	return nil, errors.New("not implemented")
}

func (m *mockS3Client) PutObject(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
	if m.putObjectFunc != nil {
		return m.putObjectFunc(input)
	}
	return nil, errors.New("not implemented")
}

func (m *mockS3Client) PutObjectAcl(input *s3.PutObjectAclInput) (*s3.PutObjectAclOutput, error) {
	if m.putObjectAclFunc != nil {
		return m.putObjectAclFunc(input)
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
		getObjectFunc: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body:         io.NopCloser(strings.NewReader(content)),
				ContentType:  aws.String(contentType),
				TagCount:     aws.Int64(tagCount),
				LastModified: aws.Time(lastModified),
				Metadata: map[string]*string{
					"key1": aws.String("value1"),
				},
			}, nil
		},
		getObjectTaggingFunc: func(input *s3.GetObjectTaggingInput) (*s3.GetObjectTaggingOutput, error) {
			return &s3.GetObjectTaggingOutput{
				TagSet: []*s3.Tag{
					{Key: aws.String("tag1"), Value: aws.String("value1")},
					{Key: aws.String("tag2"), Value: aws.String("value2")},
				},
			}, nil
		},
		getObjectAclFunc: func(input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error) {
			return &s3.GetObjectAclOutput{
				Grants: []*s3.Grant{
					{
						Grantee: &s3.Grantee{
							Type: aws.String("CanonicalUser"),
							ID:   aws.String("123456"),
						},
						Permission: aws.String("READ"),
					},
				},
			}, nil
		},
		putObjectFunc: func(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
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
	err := OverwriteS3Object(client, "test-bucket", "test-key", func(info ObjectInfo, tmpFile *os.File) (bool, error) {
		callbackCalled = true

		// Verify ObjectInfo
		if info.Bucket != "test-bucket" {
			t.Errorf("Expected bucket 'test-bucket', got %s", info.Bucket)
		}
		if info.Key != "test-key" {
			t.Errorf("Expected key 'test-key', got %s", info.Key)
		}
		if aws.StringValue(info.ContentType) != contentType {
			t.Errorf("Expected content type %s, got %s", contentType, aws.StringValue(info.ContentType))
		}

		// Verify file content
		data, err := io.ReadAll(tmpFile)
		if err != nil {
			return false, err
		}
		if string(data) != content {
			t.Errorf("Expected content '%s', got '%s'", content, string(data))
		}

		// Write modified content
		if _, err := tmpFile.Seek(0, 0); err != nil {
			return false, err
		}
		if err := tmpFile.Truncate(0); err != nil {
			return false, err
		}
		if _, err := tmpFile.WriteString("modified content"); err != nil {
			return false, err
		}

		return true, nil
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
		getObjectFunc: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(strings.NewReader("test content")),
			}, nil
		},
		putObjectFunc: func(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			putObjectCalled = true
			return &s3.PutObjectOutput{}, nil
		},
	}

	err := OverwriteS3Object(client, "test-bucket", "test-key", func(info ObjectInfo, tmpFile *os.File) (bool, error) {
		// Return false to skip overwrite
		return false, nil
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
		getObjectFunc: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(strings.NewReader("test content")),
			}, nil
		},
		getObjectAclFunc: func(input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error) {
			return &s3.GetObjectAclOutput{
				Grants: []*s3.Grant{
					{
						Grantee: &s3.Grantee{
							Type: aws.String("Group"),
							URI:  aws.String("http://acs.amazonaws.com/groups/global/AllUsers"),
						},
						Permission: aws.String("WRITE"),
					},
				},
			}, nil
		},
		putObjectFunc: func(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			// WRITE permission should not be in PutObject
			// PutObjectInput doesn't have GrantWrite field - it's only in PutObjectAclInput
			return &s3.PutObjectOutput{}, nil
		},
		putObjectAclFunc: func(input *s3.PutObjectAclInput) (*s3.PutObjectAclOutput, error) {
			putObjectAclCalled = true
			// Verify WRITE permission is restored
			if input.GrantWrite == nil || *input.GrantWrite != `uri="http://acs.amazonaws.com/groups/global/AllUsers"` {
				t.Errorf("Expected grant write, got %v", input.GrantWrite)
			}
			return &s3.PutObjectAclOutput{}, nil
		},
	}

	err := OverwriteS3Object(client, "test-bucket", "test-key", func(info ObjectInfo, tmpFile *os.File) (bool, error) {
		return true, nil
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
				m.getObjectFunc = func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					return nil, errors.New("get object failed")
				}
			},
			expectedError: "failed to get object: get object failed",
		},
		{
			name: "Callback error",
			setupMock: func(m *mockS3Client) {
				m.getObjectFunc = func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
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
				m.getObjectFunc = func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					return &s3.GetObjectOutput{
						Body:     io.NopCloser(strings.NewReader("test")),
						TagCount: aws.Int64(1),
					}, nil
				}
				m.getObjectTaggingFunc = func(input *s3.GetObjectTaggingInput) (*s3.GetObjectTaggingOutput, error) {
					return nil, errors.New("tagging failed")
				}
			},
			expectedError: "failed to get object tagging: tagging failed",
		},
		{
			name: "GetObjectAcl error",
			setupMock: func(m *mockS3Client) {
				m.getObjectFunc = func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					return &s3.GetObjectOutput{
						Body: io.NopCloser(strings.NewReader("test")),
					}, nil
				}
				m.getObjectAclFunc = func(input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error) {
					return nil, errors.New("acl failed")
				}
			},
			expectedError: "failed to get object ACL: acl failed",
		},
		{
			name: "PutObject error",
			setupMock: func(m *mockS3Client) {
				m.getObjectFunc = func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					return &s3.GetObjectOutput{
						Body: io.NopCloser(strings.NewReader("test")),
					}, nil
				}
				m.getObjectAclFunc = func(input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error) {
					return &s3.GetObjectAclOutput{}, nil
				}
				m.putObjectFunc = func(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
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

			err := OverwriteS3Object(client, "bucket", "key", func(info ObjectInfo, tmpFile *os.File) (bool, error) {
				if tt.name == "Callback error" {
					return false, errors.New("callback failed")
				}
				return true, nil
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
				getObjectFunc: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					return &s3.GetObjectOutput{
						Body:        io.NopCloser(strings.NewReader("test content")),
						ContentType: aws.String("text/plain"),
						TagCount:    aws.Int64(1),
					}, nil
				},
				getObjectTaggingFunc: func(input *s3.GetObjectTaggingInput) (*s3.GetObjectTaggingOutput, error) {
					return &s3.GetObjectTaggingOutput{
						TagSet: []*s3.Tag{
							{Key: aws.String("tag1"), Value: aws.String("value1")},
						},
					}, nil
				},
				putObjectFunc: func(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
					// Verify ACL is set
					if input.ACL == nil || *input.ACL != acl {
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

			err := OverwriteS3ObjectWithAcl(client, "test-bucket", "test-key", acl, func(info ObjectInfo, tmpFile *os.File) (bool, error) {
				// Modify content
				if _, err := tmpFile.Seek(0, 0); err != nil {
					return false, err
				}
				if err := tmpFile.Truncate(0); err != nil {
					return false, err
				}
				if _, err := tmpFile.WriteString("modified content"); err != nil {
					return false, err
				}
				return true, nil
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
		getObjectFunc: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(strings.NewReader("test content")),
			}, nil
		},
		putObjectFunc: func(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			putObjectCalled = true
			return &s3.PutObjectOutput{}, nil
		},
	}

	err := OverwriteS3ObjectWithAcl(client, "test-bucket", "test-key", "private", func(info ObjectInfo, tmpFile *os.File) (bool, error) {
		// Return false to skip overwrite
		return false, nil
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if putObjectCalled {
		t.Error("PutObject should not be called when callback returns false")
	}
}

// Test temporary file cleanup
func TestTemporaryFileCleanup(t *testing.T) {
	var tmpFileName string

	client := &mockS3Client{
		getObjectFunc: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(strings.NewReader("test content")),
			}, nil
		},
		getObjectAclFunc: func(input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error) {
			return &s3.GetObjectAclOutput{}, nil
		},
		putObjectFunc: func(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			return &s3.PutObjectOutput{}, nil
		},
	}

	// Test successful case - temp file should be cleaned up
	err := OverwriteS3Object(client, "test-bucket", "test-key", func(info ObjectInfo, tmpFile *os.File) (bool, error) {
		tmpFileName = tmpFile.Name()
		return true, nil
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check temp file is deleted
	if _, err := os.Stat(tmpFileName); !os.IsNotExist(err) {
		t.Error("Temporary file was not cleaned up")
	}

	// Test error case - temp file should still be cleaned up
	err = OverwriteS3Object(client, "test-bucket", "test-key", func(info ObjectInfo, tmpFile *os.File) (bool, error) {
		tmpFileName = tmpFile.Name()
		return false, errors.New("intentional error")
	})

	if err == nil {
		t.Error("Expected error but got none")
	}

	// Check temp file is deleted even on error
	if _, err := os.Stat(tmpFileName); !os.IsNotExist(err) {
		t.Error("Temporary file was not cleaned up after error")
	}
}

// Test temporary file permissions
func TestTemporaryFilePermissions(t *testing.T) {
	client := &mockS3Client{
		getObjectFunc: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(strings.NewReader("test content")),
			}, nil
		},
		getObjectAclFunc: func(input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error) {
			return &s3.GetObjectAclOutput{}, nil
		},
		putObjectFunc: func(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			return &s3.PutObjectOutput{}, nil
		},
	}

	err := OverwriteS3Object(client, "test-bucket", "test-key", func(info ObjectInfo, tmpFile *os.File) (bool, error) {
		// Check file permissions
		stat, err := tmpFile.Stat()
		if err != nil {
			return false, err
		}

		mode := stat.Mode()
		if mode.Perm() != 0600 {
			t.Errorf("Expected file permissions 0600, got %o", mode.Perm())
		}

		return false, nil // Skip to avoid other operations
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

// Test buildTaggingString
func TestBuildTaggingString(t *testing.T) {
	tests := []struct {
		name     string
		tags     []*s3.Tag
		expected string
	}{
		{
			name:     "Empty tags",
			tags:     []*s3.Tag{},
			expected: "",
		},
		{
			name: "Single tag",
			tags: []*s3.Tag{
				{Key: aws.String("key1"), Value: aws.String("value1")},
			},
			expected: "key1=value1",
		},
		{
			name: "Multiple tags",
			tags: []*s3.Tag{
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
	grants := []*s3.Grant{
		{
			Grantee: &s3.Grantee{
				Type: aws.String("CanonicalUser"),
				ID:   aws.String("123456"),
			},
			Permission: aws.String("READ"),
		},
		{
			Grantee: &s3.Grantee{
				Type: aws.String("Group"),
				URI:  aws.String("http://acs.amazonaws.com/groups/global/AllUsers"),
			},
			Permission: aws.String("READ"),
		},
		{
			Grantee: &s3.Grantee{
				Type:         aws.String("AmazonCustomerByEmail"),
				EmailAddress: aws.String("test@example.com"),
			},
			Permission: aws.String("WRITE"),
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