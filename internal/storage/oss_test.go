package storage

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
)

type testHTTPClient struct {
	request *http.Request
	body    string
}

func (c *testHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.request = req
	b, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	c.body = string(b)
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
}

func TestOSSRequestUsesPrivateACL(t *testing.T) {
	httpClient := &testHTTPClient{}
	cfg, err := ossConfig(OSSOptions{
		Region: "cn-hangzhou", Bucket: "test-bucket",
		AccessKeyID: "test-id", AccessKeySecret: "test-secret", SessionToken: "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.HttpClient = httpClient
	store := &OSS{client: oss.NewClient(cfg), bucket: "test-bucket"}
	if err := store.Put(context.Background(), "images/1/test.png", "image/png", []byte("image")); err != nil {
		t.Fatal(err)
	}
	if httpClient.request.Method != http.MethodPut || httpClient.request.Header.Get("x-oss-object-acl") != "private" ||
		httpClient.request.Header.Get("Content-Type") != "image/png" || httpClient.body != "image" ||
		!strings.Contains(httpClient.request.URL.Path, "/images/1/test.png") ||
		!strings.Contains(httpClient.request.Header.Get("Authorization"), "test-id") ||
		httpClient.request.Header.Get("x-oss-security-token") != "test-token" {
		t.Fatalf("unexpected SDK upload request")
	}
}

func TestOSSConfiguration(t *testing.T) {
	options := OSSOptions{Region: "cn-hangzhou", Bucket: "test-bucket"}
	if _, err := NewOSS(options); err == nil {
		t.Fatal("expected missing credentials to fail")
	}

	options.AccessKeyID = "test-id"
	if _, err := NewOSS(options); err == nil {
		t.Fatal("expected missing secret to fail")
	}
	options.AccessKeySecret = "test-secret"
	options.Endpoint = "http://example.com"
	if _, err := NewOSS(options); err == nil {
		t.Fatal("expected insecure endpoint to fail")
	}
	options.Endpoint = "https://oss-cn-hangzhou.aliyuncs.com"
	if _, err := NewOSS(options); err != nil {
		t.Fatal(err)
	}
}

func TestOSSRequestSignsConfiguredRegion(t *testing.T) {
	httpClient := &testHTTPClient{}
	cfg, err := ossConfig(OSSOptions{
		Region: "cn-heyuan", Bucket: "test-bucket",
		Endpoint:    "https://oss-cn-heyuan.aliyuncs.com",
		AccessKeyID: "test-id", AccessKeySecret: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.HttpClient = httpClient
	store := &OSS{client: oss.NewClient(cfg), bucket: "test-bucket"}
	if err := store.Put(context.Background(), "images/test.jpg", "image/jpeg", []byte("image")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(httpClient.request.Header.Get("Authorization"), "/cn-heyuan/oss/aliyun_v4_request") {
		t.Fatal("request signature does not contain the configured OSS region")
	}
}
