// internal/azure/blob.go
// Azure Blob Storage integration using the official Go SDK.
// Downloads Banner PDF release notes from a blob container to a local folder.
package azure

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

// BlobClient wraps Azure Blob Storage operations.
type BlobClient struct {
	client        *azblob.Client
	containerName string
}

// NewBlobClient creates a new BlobClient from a connection string.
func NewBlobClient(connectionString, containerName string) (*BlobClient, error) {
	client, err := azblob.NewClientFromConnectionString(connectionString, nil)
	if err != nil {
		return nil, fmt.Errorf("create blob client: %w", err)
	}
	return &BlobClient{
		client:        client,
		containerName: containerName,
	}, nil
}

// BlobInfo holds metadata about a blob document.
type BlobInfo struct {
	Name        string
	SizeBytes   int64
	ContentType string
}

// ListDocuments lists all PDFs/text files in the container (with optional prefix).
func (b *BlobClient) ListDocuments(prefix string) ([]BlobInfo, error) {
	ctx := context.Background()
	supported := map[string]bool{".pdf": true, ".txt": true, ".md": true}

	var results []BlobInfo
	pager := b.client.NewListBlobsFlatPager(b.containerName, &azblob.ListBlobsFlatOptions{
		Prefix: &prefix,
	})

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list blobs: %w", err)
		}
		for _, blob := range page.Segment.BlobItems {
			ext := strings.ToLower(filepath.Ext(*blob.Name))
			if supported[ext] {
				info := BlobInfo{Name: *blob.Name}
				if blob.Properties.ContentLength != nil {
					info.SizeBytes = *blob.Properties.ContentLength
				}
				if blob.Properties.ContentType != nil {
					info.ContentType = *blob.Properties.ContentType
				}
				results = append(results, info)
			}
		}
	}
	return results, nil
}

// DownloadDocuments downloads all supported files from the container to localDest.
// Skips files that already exist unless overwrite is true.
// Returns the list of local file paths that were downloaded.
func (b *BlobClient) DownloadDocuments(prefix, localDest string, overwrite bool) ([]string, error) {
	ctx := context.Background()

	if err := os.MkdirAll(localDest, 0755); err != nil {
		return nil, fmt.Errorf("create local dir: %w", err)
	}

	blobs, err := b.ListDocuments(prefix)
	if err != nil {
		return nil, err
	}

	if len(blobs) == 0 {
		log.Printf("No supported files found in container %q (prefix=%q)", b.containerName, prefix)
		return nil, nil
	}

	log.Printf("Found %d documents in blob storage", len(blobs))

	var downloaded []string
	for _, blob := range blobs {
		// Flatten blob path — strip any directory prefix, keep filename only
		localFilename := filepath.Base(blob.Name)
		localPath := filepath.Join(localDest, localFilename)

		if _, err := os.Stat(localPath); err == nil && !overwrite {
			log.Printf("  Skipping (already exists): %s", localFilename)
			continue
		}

		log.Printf("  Downloading: %s", localFilename)

		f, err := os.Create(localPath)
		if err != nil {
			return nil, fmt.Errorf("create file %s: %w", localPath, err)
		}

		_, err = b.client.DownloadFile(ctx, b.containerName, blob.Name, f, nil)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", blob.Name, err)
		}

		downloaded = append(downloaded, localPath)
		log.Printf("  ✓ Downloaded: %s", localFilename)
	}

	log.Printf("Downloaded %d new files to %s", len(downloaded), localDest)
	return downloaded, nil
}
