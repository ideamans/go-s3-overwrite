package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	overwrite "github.com/ideamans/go-s3-overwrite"
)

func main() {
	// Create AWS configuration
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatal(err)
	}
	svc := s3.NewFromConfig(cfg)

	// Example bucket and key - replace with your own
	bucket := "my-bucket"
	key := "data/config.json"

	// Example: Format JSON file and preserve all attributes
	err = overwrite.OverwriteS3Object(context.Background(), svc, bucket, key,
		func(info overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
			fmt.Printf("Processing: %s/%s (size: %d bytes)\n",
				info.Bucket, info.Key, *info.ContentLength)

			// Skip files larger than 10MB
			if *info.ContentLength > 10*1024*1024 {
				fmt.Println("Skipping: file too large")
				return "", false, nil
			}

			// Read JSON content
			data, err := os.ReadFile(srcFilePath)
			if err != nil {
				return "", false, err
			}

			// Parse JSON
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

	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Successfully formatted JSON file")

	// Example 2: Set public-read ACL while preserving tags
	publicKey := "public/data.json"
	err = overwrite.OverwriteS3ObjectWithAcl(context.Background(), svc, bucket, publicKey, "public-read",
		func(info overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
			fmt.Printf("Making public: %s/%s\n", info.Bucket, info.Key)

			// Just changing ACL, no content modification needed
			// Return the same file path to preserve content

			// Add metadata to track when it was made public
			if info.Metadata == nil {
				info.Metadata = make(map[string]*string)
			}
			info.Metadata["made-public"] = aws.String(time.Now().Format(time.RFC3339))

			return srcFilePath, false, nil
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

func processLogs(svc *s3.Client, bucket, prefix string) {
	// List objects with prefix
	resp, err := svc.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
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

		err := overwrite.OverwriteS3Object(context.Background(), svc, bucket, *obj.Key,
			func(info overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
				// Example: Add processing timestamp to logs
				content, err := os.ReadFile(srcFilePath)
				if err != nil {
					return "", false, err
				}

				// Add timestamp header
				header := fmt.Sprintf("# Processed at %s\n", time.Now().Format(time.RFC3339))
				newContent := append([]byte(header), content...)

				// Create new file with processed content
				processedFile, err := os.CreateTemp("", "processed-*.log")
				if err != nil {
					return "", false, err
				}
				defer processedFile.Close()

				if _, err := processedFile.Write(newContent); err != nil {
					os.Remove(processedFile.Name())
					return "", false, err
				}

				// Update metadata
				if info.Metadata == nil {
					info.Metadata = make(map[string]*string)
				}
				info.Metadata["processed"] = aws.String("true")

				return processedFile.Name(), true, nil
			})

		if err != nil {
			log.Printf("Error processing %s: %v", *obj.Key, err)
		} else {
			fmt.Printf("Processed: %s\n", *obj.Key)
		}
	}
}
