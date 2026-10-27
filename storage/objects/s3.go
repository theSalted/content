package objects

import (
	"bytes"
	"content/media"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Config struct{ Endpoint, Region, Bucket, AccessKey, SecretKey, SessionToken, URLStyle string }
type S3 struct {
	client    *s3.Client
	bucket    string
	transport *http.Transport
}

var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func NewS3(c S3Config) (*S3, error) {
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return nil, errors.New("S3 requires an explicit HTTPS endpoint, or loopback HTTP for tests")
	}
	if !bucketName.MatchString(c.Bucket) || strings.Contains(c.Bucket, "..") || c.Region == "" || c.AccessKey == "" || c.SecretKey == "" {
		return nil, errors.New("S3 requires bucket, region and explicit server credentials")
	}
	if c.URLStyle != "" && c.URLStyle != "path" && c.URLStyle != "virtual-host" {
		return nil, errors.New("S3 URL style must be path or virtual-host")
	}
	if c.URLStyle == "virtual-host" && (strings.Contains(c.Bucket, ".") || (u.Path != "" && u.Path != "/")) {
		return nil, errors.New("virtual-host S3 requires a DNS-safe undotted bucket and root endpoint")
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 15 * time.Second, IdleConnTimeout: 30 * time.Second}
	client := &http.Client{Transport: transport, Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("S3 redirects are refused") }}
	// No environment/shared-profile, subprocess, container/IMDS or Supabase SDK
	// credential discovery. Only this server's explicitly configured credentials.
	credentials := aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: c.AccessKey, SecretAccessKey: c.SecretKey, SessionToken: c.SessionToken, Source: "content-explicit-media-config"}, nil
	}))
	sdk := s3.New(s3.Options{BaseEndpoint: &c.Endpoint, Region: c.Region, UsePathStyle: c.URLStyle != "virtual-host", Credentials: credentials, HTTPClient: client, RetryMaxAttempts: 2, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired})
	return &S3{client: sdk, bucket: c.Bucket, transport: transport}, nil
}
func (s *S3) Close() { s.transport.CloseIdleConnections() }
func (s *S3) Put(ctx context.Context, key string, data []byte, mime string) error {
	if !media.ValidObjectKey(key) || len(data) > media.MaxBytes {
		return media.ErrInvalid
	}
	cache := "private, no-store"
	_, e := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, Body: bytes.NewReader(data), ContentType: &mime, CacheControl: &cache})
	if e != nil {
		return media.ErrUnavailable
	}
	return nil
}
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if !media.ValidObjectKey(key) {
		return nil, media.ErrInvalid
	}
	out, e := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if e != nil {
		return nil, media.ErrUnavailable
	}
	return out.Body, nil
}
func (s *S3) Delete(ctx context.Context, key string) error {
	if !media.ValidObjectKey(key) {
		return media.ErrInvalid
	}
	_, e := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	if e != nil {
		return media.ErrUnavailable
	}
	return nil
}
