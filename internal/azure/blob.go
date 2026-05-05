// internal/azure/blob.go
// Azure Blob Storage integration using the official Go SDK.
// Downloads Banner PDF release notes from a blob container to a local folder.
package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"citesearch/internal/blobstore"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
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

type BlobInfo = blobstore.Info

// ListDocuments lists all PDFs/text files in the container (with optional prefix).
func (b *BlobClient) ListDocuments(prefix string) ([]BlobInfo, error) {
	return b.listDocuments(context.Background(), prefix)
}

func (b *BlobClient) listDocuments(ctx context.Context, prefix string) ([]BlobInfo, error) {
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
			if isSupportedBlobDocument(*blob.Name) {
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

func isSupportedBlobDocument(blobName string) bool {
	if strings.HasSuffix(strings.ToLower(blobName), ".chunks.json") {
		return false
	}
	switch strings.ToLower(filepath.Ext(blobName)) {
	case ".pdf", ".txt", ".md":
		return true
	default:
		return false
	}
}

// Upload stores a single blob at the provided path without flattening or rewriting it.
func (b *BlobClient) Upload(ctx context.Context, blobPath string, content io.Reader, contentType string) error {
	opts := &azblob.UploadStreamOptions{}
	if contentType != "" {
		opts.HTTPHeaders = &blob.HTTPHeaders{BlobContentType: &contentType}
	}
	if _, err := b.client.UploadStream(ctx, b.containerName, blobPath, content, opts); err != nil {
		return fmt.Errorf("upload blob %s: %w", blobPath, err)
	}
	return nil
}

// Download writes a single blob to dest.
func (b *BlobClient) Download(ctx context.Context, blobPath string, dest io.Writer) error {
	resp, err := b.client.DownloadStream(ctx, b.containerName, blobPath, nil)
	if err != nil {
		return fmt.Errorf("download blob %s: %w", blobPath, err)
	}
	body := resp.NewRetryReader(ctx, nil)
	defer body.Close()

	if _, err := io.Copy(dest, body); err != nil {
		return fmt.Errorf("read blob %s: %w", blobPath, err)
	}
	return nil
}

// Exists reports whether a blob exists.
func (b *BlobClient) Exists(ctx context.Context, blobPath string) (bool, error) {
	resp, err := b.client.DownloadStream(ctx, b.containerName, blobPath, nil)
	if err == nil {
		_ = resp.Body.Close()
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("check blob %s: %w", blobPath, err)
}

// Delete removes a single blob.
func (b *BlobClient) Delete(ctx context.Context, blobPath string) error {
	if _, err := b.client.DeleteBlob(ctx, b.containerName, blobPath, nil); err != nil {
		return fmt.Errorf("delete blob %s: %w", blobPath, err)
	}
	return nil
}

// ReadJSON downloads a blob and decodes its JSON body.
func (b *BlobClient) ReadJSON(ctx context.Context, blobPath string, dest any) error {
	var buf bytes.Buffer
	if err := b.Download(ctx, blobPath, &buf); err != nil {
		return err
	}
	if err := json.NewDecoder(&buf).Decode(dest); err != nil {
		return fmt.Errorf("decode json blob %s: %w", blobPath, err)
	}
	return nil
}

// WriteJSON encodes value as JSON and uploads it to a blob.
func (b *BlobClient) WriteJSON(ctx context.Context, blobPath string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode json blob %s: %w", blobPath, err)
	}
	return b.Upload(ctx, blobPath, bytes.NewReader(data), "application/json")
}

// List returns blob objects under prefix for upload package orchestration.
func (b *BlobClient) List(ctx context.Context, prefix string) ([]BlobInfo, error) {
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
	return results, nil
}

func isNotFound(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound
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
