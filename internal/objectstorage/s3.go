// Package objectstorage reads private runtime configuration from object storage.
package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/Omnific-Tech/herald/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const maxObjectBytes = 1 << 20

// Read fetches one object from the configured private S3-compatible bucket.
func Read(ctx context.Context, cfg config.ObjectStorage, key string) ([]byte, error) {
	if cfg.Provider != "s3" {
		return nil, fmt.Errorf("unsupported object storage provider %q", cfg.Provider)
	}
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.Region == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("S3_ENDPOINT, S3_BUCKET, AWS_DEFAULT_REGION, AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY are required")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("configure s3: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(cfg.Endpoint)
		options.UsePathStyle = true
	})
	response, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(cfg.Bucket), Key: aws.String(key)})
	if err != nil {
		return nil, fmt.Errorf("read s3://%s/%s: %w", cfg.Bucket, key, err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxObjectBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read credentials object: %w", err)
	}
	if len(body) > maxObjectBytes {
		return nil, errors.New("credentials object exceeds 1 MiB")
	}
	return body, nil
}
