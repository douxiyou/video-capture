package storage

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

type OSS struct {
	client *oss.Client
	bucket string
}

type OSSOptions struct {
	Region, Bucket, Endpoint     string
	AccessKeyID, AccessKeySecret string
	SessionToken                 string
}

func ossConfig(options OSSOptions) (*oss.Config, error) {
	if strings.TrimSpace(options.Region) == "" || strings.TrimSpace(options.Bucket) == "" {
		return nil, fmt.Errorf("OSS region and bucket are required")
	}
	if strings.TrimSpace(options.AccessKeyID) == "" || strings.TrimSpace(options.AccessKeySecret) == "" {
		return nil, fmt.Errorf("oss.access_key_id and oss.access_key_secret are required")
	}
	if options.Endpoint != "" {
		u, err := url.Parse(options.Endpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("oss.endpoint must be an HTTPS origin")
		}
	}
	provider := credentials.NewStaticCredentialsProvider(options.AccessKeyID, options.AccessKeySecret, options.SessionToken)
	cfg := oss.LoadDefaultConfig().WithCredentialsProvider(provider).WithRegion(options.Region)
	if options.Endpoint != "" {
		cfg = cfg.WithEndpoint(options.Endpoint)
	}
	return cfg, nil
}

func NewOSS(options OSSOptions) (*OSS, error) {
	cfg, err := ossConfig(options)
	if err != nil {
		return nil, err
	}
	return &OSS{client: oss.NewClient(cfg), bucket: options.Bucket}, nil
}

func (s *OSS) Put(ctx context.Context, key, contentType string, data []byte) error {
	size := int64(len(data))
	_, err := s.client.PutObject(ctx, &oss.PutObjectRequest{
		Bucket: oss.Ptr(s.bucket), Key: oss.Ptr(key), Body: bytes.NewReader(data),
		ContentLength: &size, ContentType: oss.Ptr(contentType), Acl: oss.ObjectACLPrivate,
	})
	if err != nil {
		return fmt.Errorf("put OSS object: %w", err)
	}
	return nil
}
