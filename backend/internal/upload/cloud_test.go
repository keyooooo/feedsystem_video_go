package upload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	sts "github.com/alibabacloud-go/sts-20150401/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

const (
	testCoverStaging = "direct-upload/staging/cover/1234567890abcdef1234567890abcdef"
	testCoverAsset   = "direct-upload/assets/cover/1234567890abcdef1234567890abcdef"
)

type testAssumer func(*sts.AssumeRoleRequest) (*sts.AssumeRoleResponse, error)

func (s testAssumer) AssumeRoleWithContext(_ context.Context, req *sts.AssumeRoleRequest, _ *dara.RuntimeOptions) (*sts.AssumeRoleResponse, error) {
	return s(req)
}

func TestSTSGrantIsScopedToOneStagingObject(t *testing.T) {
	for _, kind := range []string{"video", "cover", "avatar"} {
		t.Run(kind, func(t *testing.T) {
			key := strings.Replace(testCoverStaging, "/cover/", "/"+kind+"/", 1)
			now := time.Now().UTC().Truncate(time.Second)
			cloud := alibabaCloud{
				config:   CloudConfig{Bucket: "demo", Region: "cn-guangzhou"},
				endpoint: "https://oss-cn-guangzhou.aliyuncs.com",
				sts: testAssumer(func(req *sts.AssumeRoleRequest) (*sts.AssumeRoleResponse, error) {
					if *req.DurationSeconds != 900 {
						t.Fatal("STS requested an unexpected lifetime")
					}
					var policy struct {
						Statement []struct {
							Effect   string
							Action   []string
							Resource []string
						}
					}
					if err := json.Unmarshal([]byte(*req.Policy), &policy); err != nil {
						t.Fatal(err)
					}
					if len(policy.Statement) != 1 {
						t.Fatal("expected one grant statement")
					}
					statement := policy.Statement[0]
					if statement.Effect != "Allow" || len(statement.Resource) != 1 || statement.Resource[0] != "acs:oss:*:*:demo/"+key {
						t.Fatalf("STS policy resource does not match the exact staging key: %s", *req.Policy)
					}
					want := "oss:PutObject"
					if kind == "video" {
						want += ",oss:ListParts,oss:AbortMultipartUpload"
					}
					if strings.Join(statement.Action, ",") != want {
						t.Fatalf("unexpected STS operations: %v", statement.Action)
					}
					return &sts.AssumeRoleResponse{Body: &sts.AssumeRoleResponseBody{Credentials: &sts.AssumeRoleResponseBodyCredentials{
						AccessKeyId: oss.Ptr("test-temporary-id"), AccessKeySecret: oss.Ptr("test-temporary-secret"),
						SecurityToken: oss.Ptr("test-token"), Expiration: oss.Ptr(now.Add(grantDuration).Format(time.RFC3339)),
					}}}, nil
				}),
			}
			grant, err := cloud.Grant(context.Background(), key, kind)
			if err != nil || grant.Key != key || grant.SecurityToken == "" || !grant.ExpiresAt.Equal(now.Add(grantDuration)) {
				t.Fatalf("grant did not use expected STS response: %v", err)
			}
		})
	}
	cloud := alibabaCloud{}
	for _, key := range []string{testCoverAsset, "direct-upload/staging/cover/*", testCoverStaging + "/other"} {
		if _, err := cloud.Grant(context.Background(), key, "cover"); err == nil {
			t.Fatalf("signed invalid key %q", key)
		}
	}
}

func TestSTSRejectsExpiredCredentials(t *testing.T) {
	cloud := alibabaCloud{sts: testAssumer(func(*sts.AssumeRoleRequest) (*sts.AssumeRoleResponse, error) {
		return &sts.AssumeRoleResponse{Body: &sts.AssumeRoleResponseBody{Credentials: &sts.AssumeRoleResponseBodyCredentials{
			AccessKeyId: oss.Ptr("test-id"), AccessKeySecret: oss.Ptr("test-secret"),
			SecurityToken: oss.Ptr("test-token"), Expiration: oss.Ptr(time.Now().Add(-time.Minute).Format(time.RFC3339)),
		}}}, nil
	})}
	if _, err := cloud.Grant(context.Background(), testCoverStaging, "cover"); err == nil {
		t.Fatal("expired STS credentials were returned")
	}
}

type objectStub struct {
	head func(*oss.HeadObjectRequest) (*oss.HeadObjectResult, error)
	get  func(*oss.GetObjectRequest) (*oss.GetObjectResult, error)
	copy func(*oss.CopyObjectRequest) (*oss.CopyObjectResult, error)
}

func (o objectStub) HeadObject(_ context.Context, r *oss.HeadObjectRequest, _ ...func(*oss.Options)) (*oss.HeadObjectResult, error) {
	return o.head(r)
}

func (o objectStub) GetObject(_ context.Context, r *oss.GetObjectRequest, _ ...func(*oss.Options)) (*oss.GetObjectResult, error) {
	return o.get(r)
}

func (o objectStub) CopyObject(_ context.Context, r *oss.CopyObjectRequest, _ ...func(*oss.Options)) (*oss.CopyObjectResult, error) {
	return o.copy(r)
}

func (o objectStub) DeleteObject(context.Context, *oss.DeleteObjectRequest, ...func(*oss.Options)) (*oss.DeleteObjectResult, error) {
	panic("unexpected DeleteObject")
}

func TestVerifyChecksImmutableCopyAfterLostResponse(t *testing.T) {
	original := pngImage(t)
	staging := append([]byte(nil), original...)
	var final []byte
	stub := objectStub{
		head: func(req *oss.HeadObjectRequest) (*oss.HeadObjectResult, error) {
			if *req.Bucket != "demo" {
				t.Fatal("metadata request used an unexpected bucket")
			}
			if *req.Key == testCoverStaging {
				return &oss.HeadObjectResult{ContentLength: int64(len(staging)), ETag: oss.Ptr("\"staging-2\""),
					ObjectType: oss.Ptr("Multipart"), ContentType: oss.Ptr("text/html")}, nil
			}
			if *req.Key != testCoverAsset {
				t.Fatalf("unexpected object key %q", *req.Key)
			}
			if final == nil {
				return nil, &oss.ServiceError{StatusCode: 404, Code: "NoSuchKey"}
			}
			return &oss.HeadObjectResult{ContentLength: int64(len(final)), ETag: oss.Ptr("\"final\""),
				ObjectType: oss.Ptr("Normal"), ContentType: oss.Ptr("image/png")}, nil
		},
		copy: func(req *oss.CopyObjectRequest) (*oss.CopyObjectResult, error) {
			if *req.Bucket != "demo" || *req.SourceBucket != "demo" || *req.SourceKey != testCoverStaging ||
				*req.Key != testCoverAsset || *req.IfMatch != "\"staging-2\"" || *req.ForbidOverwrite != "true" ||
				*req.MetadataDirective != "REPLACE" || *req.ContentType != "image/png" || req.StorageClass != oss.StorageClassStandard {
				t.Fatalf("copy did not fix the source revision and normalize metadata: %+v", req)
			}
			if final != nil {
				t.Fatal("attempted overwrite of immutable destination")
			}
			final = append([]byte(nil), staging...)
			// These simulate a lost cloud response and an overwrite using a
			// credential still valid for staging after the verified copy.
			staging = bytes.Repeat([]byte{'x'}, len(staging))
			return nil, errors.New("copy response lost")
		},
		get: func(req *oss.GetObjectRequest) (*oss.GetObjectResult, error) {
			if *req.Bucket != "demo" || *req.Key != testCoverAsset || *req.IfMatch != "\"final\"" {
				t.Fatalf("verification did not read the pinned final object: %+v", req)
			}
			return &oss.GetObjectResult{Body: io.NopCloser(bytes.NewReader(final)),
				ContentLength: int64(len(final)), ObjectType: oss.Ptr("Normal")}, nil
		},
	}
	cloud := alibabaCloud{config: CloudConfig{Bucket: "demo"}, internal: stub}
	for range 2 {
		if err := cloud.Verify(context.Background(), testCoverStaging, testCoverAsset, "cover", "image/png", int64(len(original))); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(final, original) {
		t.Fatal("immutable object changed")
	}
}

func TestVerifyRejectsMissingOversizeOrSymbolicObject(t *testing.T) {
	for _, test := range []struct {
		name string
		head func() (*oss.HeadObjectResult, error)
		want error
	}{
		{"absent", func() (*oss.HeadObjectResult, error) { return nil, &oss.ServiceError{StatusCode: 404} }, ErrObjectPending},
		{"oversize", func() (*oss.HeadObjectResult, error) {
			return &oss.HeadObjectResult{ContentLength: maxCoverBytes + 1, ObjectType: oss.Ptr("Normal")}, nil
		}, ErrInvalidMedia},
		{"symlink", func() (*oss.HeadObjectResult, error) {
			return &oss.HeadObjectResult{ContentLength: 8, ObjectType: oss.Ptr("Symlink"), ETag: oss.Ptr("\"valid\"")}, nil
		}, ErrInvalidMedia},
		{"unsupported storage class", func() (*oss.HeadObjectResult, error) {
			return &oss.HeadObjectResult{ContentLength: 8, ObjectType: oss.Ptr("Normal"),
				StorageClass: oss.Ptr("Archive"), ETag: oss.Ptr("\"valid\"")}, nil
		}, ErrInvalidMedia},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := objectStub{head: func(req *oss.HeadObjectRequest) (*oss.HeadObjectResult, error) {
				if *req.Key == testCoverAsset {
					return nil, &oss.ServiceError{StatusCode: 404}
				}
				return test.head()
			}}
			cloud := alibabaCloud{internal: stub}
			if err := cloud.Verify(context.Background(), testCoverStaging, testCoverAsset, "cover", "image/png", 8); !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}
}

func TestVerifyRetriesWhenSourceChangedBeforeCopy(t *testing.T) {
	stub := objectStub{
		head: func(req *oss.HeadObjectRequest) (*oss.HeadObjectResult, error) {
			if *req.Key == testCoverAsset {
				return nil, &oss.ServiceError{StatusCode: 404}
			}
			return &oss.HeadObjectResult{ContentLength: 8, ObjectType: oss.Ptr("Normal"), ETag: oss.Ptr("\"old\"")}, nil
		},
		copy: func(req *oss.CopyObjectRequest) (*oss.CopyObjectResult, error) {
			if oss.ToString(req.IfMatch) != "\"old\"" {
				t.Fatal("copy did not include staging ETag")
			}
			return nil, &oss.ServiceError{StatusCode: 412}
		},
	}
	cloud := alibabaCloud{internal: stub}
	if err := cloud.Verify(context.Background(), testCoverStaging, testCoverAsset, "cover", "image/png", 8); !errors.Is(err, ErrObjectPending) {
		t.Fatalf("expected retry for a changed staging object, got %v", err)
	}
}

func TestVerifyRejectsForgedFinalBytes(t *testing.T) {
	data := []byte("<svg />")
	stub := objectStub{
		head: func(*oss.HeadObjectRequest) (*oss.HeadObjectResult, error) {
			return &oss.HeadObjectResult{ContentLength: int64(len(data)), ObjectType: oss.Ptr("Normal"),
				ContentType: oss.Ptr("image/png"), ETag: oss.Ptr("\"final\"")}, nil
		},
		get: func(*oss.GetObjectRequest) (*oss.GetObjectResult, error) {
			return &oss.GetObjectResult{ContentLength: int64(len(data)), ObjectType: oss.Ptr("Normal"),
				Body: io.NopCloser(bytes.NewReader(data))}, nil
		},
	}
	cloud := alibabaCloud{internal: stub}
	if err := cloud.Verify(context.Background(), testCoverStaging, testCoverAsset, "cover", "image/png", int64(len(data))); !errors.Is(err, ErrInvalidMedia) {
		t.Fatalf("expected MIME spoof to be rejected, got %v", err)
	}
}

func TestPlaybackSignsPublicHTTPSGetAndHead(t *testing.T) {
	cloud := alibabaCloud{
		config: CloudConfig{Bucket: "test-playback"},
		playback: oss.NewClient(oss.LoadDefaultConfig().WithRegion("cn-guangzhou").
			WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test-id", "test-secret"))),
	}
	signed := map[string]string{}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		address, err := cloud.Sign(context.Background(), testCoverAsset, method)
		if err != nil {
			t.Fatal(err)
		}
		target, err := url.Parse(address)
		if err != nil || target.Scheme != "https" || target.Host != "test-playback.oss-cn-guangzhou.aliyuncs.com" ||
			target.Path != "/"+testCoverAsset || target.Query().Get("x-oss-expires") != "900" {
			t.Fatal("playback did not sign the public OSS URL for 15 minutes")
		}
		signed[method] = target.Query().Get("x-oss-signature")
	}
	if signed[http.MethodGet] == "" || signed[http.MethodGet] == signed[http.MethodHead] {
		t.Fatal("GET and HEAD require distinct signatures")
	}
	if _, err := cloud.Sign(context.Background(), testCoverStaging, http.MethodGet); err == nil {
		t.Fatal("staging playback signed")
	}
}

func TestUploadCustomHTTPSOrigin(t *testing.T) {
	cfg := CloudConfig{
		Region: "cn-guangzhou", Bucket: "demo-bucket",
		UploadRoleARN: "acs:ram::123456789:role/oss-upload",
		AccessKeyID:   "test", AccessKeySecret: "test", ProbePath: "sh",
		UploadEndpoint: "https://media.example.invalid",
	}
	cloud, err := NewAlibabaCloud(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cloud.(*alibabaCloud).endpoint != cfg.UploadEndpoint {
		t.Fatal("STS grant must use the configured custom origin")
	}
	signed, err := cloud.Sign(context.Background(), testCoverAsset, http.MethodGet)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(signed, cfg.UploadEndpoint+"/"+testCoverAsset+"?") {
		t.Fatal("playback must default to the same custom origin")
	}
	cfg.UploadEndpoint = "https://oss-cn-guangzhou-internal.aliyuncs.com"
	if _, err := NewAlibabaCloud(cfg); err == nil {
		t.Fatal("browser must not receive an internal endpoint")
	}
	cfg.UploadEndpoint = "https://media.example.invalid/extra"
	if _, err := NewAlibabaCloud(cfg); err == nil {
		t.Fatal("browser endpoint must be an origin")
	}
}
