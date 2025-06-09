package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	overwrite "github.com/ideamans/go-s3-overwrite"
)

func main() {
	// Create AWS session
	sess := session.Must(session.NewSession())
	svc := s3.New(sess)

	// Example bucket and key - replace with your own
	bucket := "my-bucket"
	key := "data/config.json"

	// Example: Format JSON file and preserve all attributes
	err := overwrite.OverwriteS3Object(svc, bucket, key,
		func(info overwrite.ObjectInfo, tmpFile *os.File) (bool, error) {
			fmt.Printf("Processing: %s/%s (size: %d bytes)\n",
				info.Bucket, info.Key, *info.ContentLength)

			// Skip files larger than 10MB
			if *info.ContentLength > 10*1024*1024 {
				fmt.Println("Skipping: file too large")
				return false, nil
			}

			// Read JSON content
			data, err := io.ReadAll(tmpFile)
			if err != nil {
				return false, err
			}

			// Parse JSON
			var jsonData interface{}
			if err := json.Unmarshal(data, &jsonData); err != nil {
				return false, fmt.Errorf("invalid JSON: %w", err)
			}

			// Format with indentation
			formatted, err := json.MarshalIndent(jsonData, "", "  ")
			if err != nil {
				return false, err
			}

			// Add metadata
			if info.Metadata == nil {
				info.Metadata = make(map[string]*string)
			}
			info.Metadata["formatted"] = aws.String("true")
			info.Metadata["formatted-at"] = aws.String(time.Now().Format(time.RFC3339))

			// Write formatted JSON back to temp file
			if _, err := tmpFile.Seek(0, 0); err != nil {
				return false, err
			}
			if err := tmpFile.Truncate(0); err != nil {
				return false, err
			}
			_, err = tmpFile.Write(formatted)

			return true, err
		})

	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Successfully formatted JSON file")

	// Example 2: Set public-read ACL while preserving tags
	publicKey := "public/data.json"
	err = overwrite.OverwriteS3ObjectWithAcl(svc, bucket, publicKey, "public-read",
		func(info overwrite.ObjectInfo, tmpFile *os.File) (bool, error) {
			fmt.Printf("Making public: %s/%s\n", info.Bucket, info.Key)

			// Just changing ACL, no content modification needed
			// But we could modify content here if needed

			// Add metadata to track when it was made public
			if info.Metadata == nil {
				info.Metadata = make(map[string]*string)
			}
			info.Metadata["made-public"] = aws.String(time.Now().Format(time.RFC3339))

			return true, nil
		})

	if err != nil {
		log.Printf("Error making file public: %v", err)
	} else {
		fmt.Println("Successfully made file public")
	}

	// Example 3: Process multiple files
	prefix := "logs/"
	processLogs(svc, bucket, prefix)
}

func processLogs(svc *s3.S3, bucket, prefix string) {
	// List objects with prefix
	resp, err := svc.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		log.Printf("Error listing objects: %v", err)
		return
	}

	// Process each log file
	for _, obj := range resp.Contents {
		if *obj.Size == 0 {
			continue // Skip empty files
		}

		err := overwrite.OverwriteS3Object(svc, bucket, *obj.Key,
			func(info overwrite.ObjectInfo, tmpFile *os.File) (bool, error) {
				// Example: Add processing timestamp to logs
				content, err := io.ReadAll(tmpFile)
				if err != nil {
					return false, err
				}

				// Add timestamp header
				header := fmt.Sprintf("# Processed at %s\n", time.Now().Format(time.RFC3339))
				newContent := append([]byte(header), content...)

				// Write back
				if _, err := tmpFile.Seek(0, 0); err != nil {
					return false, err
				}
				if err := tmpFile.Truncate(0); err != nil {
					return false, err
				}
				_, err = tmpFile.Write(newContent)

				// Update metadata
				if info.Metadata == nil {
					info.Metadata = make(map[string]*string)
				}
				info.Metadata["processed"] = aws.String("true")

				return true, err
			})

		if err != nil {
			log.Printf("Error processing %s: %v", *obj.Key, err)
		} else {
			fmt.Printf("Processed: %s\n", *obj.Key)
		}
	}
}
