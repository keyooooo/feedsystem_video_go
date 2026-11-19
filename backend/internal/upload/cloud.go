package upload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	sts "github.com/alibabacloud-go/sts-20150401/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

var (
	ErrObjectPending = errors.New("the uploaded object is not ready")
	ErrInvalidMedia  = errors.New("invalid uploaded media")
)

const grantDuration = 15 * time.Minute

type Grant struct {
	Bucket          string    `json:"bucket"`
	Region          string    `json:"region"`
	Endpoint        string    `json:"endpoint"`
	Key             string    `json:"key"`
	AccessKeyID     string    `json:"access_key_id"`
	AccessKeySecret string    `json:"access_key_secret"`
	SecurityToken   string    `json:"security_token"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type Cloud interface {
	Grant(ctx context.Context, key, kind string) (Grant, error)
	Verify(ctx context.Context, stagingKey, finalKey, kind, contentType string, size int64) error
	Sign(ctx context.Context, key, method string) (string, error)
	Delete(ctx context.Context, key string) error
}

type CloudConfig struct {
	Region, Bucket, UploadRoleARN, AccessKeyID, AccessKeySecret, PlaybackEndpoint, ProbePath string
}

type ossObjects interface {
	HeadObject(context.Context, *oss.HeadObjectRequest, ...func(*oss.Options)) (*oss.HeadObjectResult, error)
	CopyObject(context.Context, *oss.CopyObjectRequest, ...func(*oss.Options)) (*oss.CopyObjectResult, error)
	GetObject(context.Context, *oss.GetObjectRequest, ...func(*oss.Options)) (*oss.GetObjectResult, error)
	DeleteObject(context.Context, *oss.DeleteObjectRequest, ...func(*oss.Options)) (*oss.DeleteObjectResult, error)
}

type roleAssumer interface {
	AssumeRoleWithContext(context.Context, *sts.AssumeRoleRequest, *dara.RuntimeOptions) (*sts.AssumeRoleResponse, error)
}

type alibabaCloud struct {
	config   CloudConfig
	endpoint string
	internal ossObjects
	playback *oss.Client
	sts      roleAssumer
}

var (
	regionPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)+$`)
	bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	rolePattern   = regexp.MustCompile(`^acs:ram::[0-9]+:role/[a-zA-Z0-9._-]+$`)
	objectPattern = regexp.MustCompile(`^direct-upload/(staging|assets)/(video|cover|avatar)/[0-9a-f]{32}$`)
)

func NewAlibabaCloud(cfg CloudConfig) (Cloud, error) {
	if !regionPattern.MatchString(cfg.Region) || !bucketPattern.MatchString(cfg.Bucket) ||
		!rolePattern.MatchString(cfg.UploadRoleARN) || cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return nil, errors.New("OSS region, bucket, upload role ARN and server access credentials are required")
	}
	endpoint := "https://oss-" + cfg.Region + ".aliyuncs.com"
	provider := credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.AccessKeySecret)
	internal := oss.NewClient(oss.LoadDefaultConfig().WithRegion(cfg.Region).WithUseInternalEndpoint(true).
		WithCredentialsProvider(provider).WithConnectTimeout(5 * time.Second).WithReadWriteTimeout(20 * time.Second))
	playbackConfig := oss.LoadDefaultConfig().WithRegion(cfg.Region).WithCredentialsProvider(provider)
	if cfg.PlaybackEndpoint != "" {
		u, err := url.Parse(cfg.PlaybackEndpoint)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Port() != "" ||
			(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
			strings.Contains(strings.ToLower(u.Hostname()), "-internal.") {
			return nil, errors.New("OSS playback endpoint must be a public HTTPS origin")
		}
		playbackConfig.WithEndpoint("https://" + u.Host).WithUseCName(true)
	}
	client, err := sts.NewClient(&openapi.Config{
		AccessKeyId: oss.Ptr(cfg.AccessKeyID), AccessKeySecret: oss.Ptr(cfg.AccessKeySecret),
		RegionId: oss.Ptr(cfg.Region), Endpoint: oss.Ptr("sts." + cfg.Region + ".aliyuncs.com"),
		Protocol: oss.Ptr("HTTPS"), ConnectTimeout: oss.Ptr(5000), ReadTimeout: oss.Ptr(10000),
	})
	if err != nil {
		return nil, errors.New("initialize STS client")
	}
	if cfg.ProbePath == "" {
		cfg.ProbePath = "ffprobe"
	}
	cfg.ProbePath, err = exec.LookPath(cfg.ProbePath)
	if err != nil {
		return nil, errors.New("ffprobe is required when direct OSS uploads are enabled")
	}
	return &alibabaCloud{
		config: cfg, endpoint: endpoint, internal: internal, playback: oss.NewClient(playbackConfig),
		sts: client,
	}, nil
}

func (c *alibabaCloud) Grant(ctx context.Context, key, kind string) (Grant, error) {
	if !objectPattern.MatchString(key) || !strings.HasPrefix(key, "direct-upload/staging/"+kind+"/") {
		return Grant{}, errors.New("invalid staging key for STS")
	}
	actions := []string{"oss:PutObject"}
	if kind == "video" {
		actions = append(actions, "oss:ListParts", "oss:AbortMultipartUpload")
	}
	type statement struct {
		Effect   string   `json:"Effect"`
		Action   []string `json:"Action"`
		Resource []string `json:"Resource"`
	}
	policy, err := json.Marshal(struct {
		Version   string      `json:"Version"`
		Statement []statement `json:"Statement"`
	}{
		Version:   "1",
		Statement: []statement{{"Allow", actions, []string{"acs:oss:*:*:" + c.config.Bucket + "/" + key}}},
	})
	if err != nil {
		return Grant{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	result, err := c.sts.AssumeRoleWithContext(ctx, new(sts.AssumeRoleRequest).
		SetRoleArn(c.config.UploadRoleARN).SetRoleSessionName("upload-"+key[len(key)-24:]).
		SetDurationSeconds(int64(grantDuration/time.Second)).SetPolicy(string(policy)), &dara.RuntimeOptions{})
	if err != nil {
		return Grant{}, errors.New("OSS upload STS AssumeRole failed")
	}
	if result == nil || result.Body == nil || result.Body.Credentials == nil {
		return Grant{}, errors.New("OSS upload STS returned no credentials")
	}
	value := result.Body.Credentials
	expires, err := time.Parse(time.RFC3339, oss.ToString(value.Expiration))
	if err != nil || !expires.After(time.Now()) || expires.After(time.Now().Add(grantDuration+time.Minute)) ||
		oss.ToString(value.AccessKeyId) == "" || oss.ToString(value.AccessKeySecret) == "" || oss.ToString(value.SecurityToken) == "" {
		return Grant{}, errors.New("OSS upload STS returned invalid credentials or lifetime")
	}
	return Grant{
		Bucket: c.config.Bucket, Region: c.config.Region, Endpoint: c.endpoint, Key: key,
		AccessKeyID: *value.AccessKeyId, AccessKeySecret: *value.AccessKeySecret,
		SecurityToken: *value.SecurityToken, ExpiresAt: expires.UTC(),
	}, nil
}

func (c *alibabaCloud) Verify(ctx context.Context, stagingKey, finalKey, kind, contentType string, size int64) error {
	if !objectPattern.MatchString(stagingKey) || !strings.HasPrefix(stagingKey, "direct-upload/staging/"+kind+"/") ||
		finalKey != "direct-upload/assets/"+strings.TrimPrefix(stagingKey, "direct-upload/staging/") {
		return errors.New("invalid object keys for verification")
	}
	if !validMediaDeclaration(kind, contentType, size) {
		return ErrInvalidMedia
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()

	final, err := c.head(ctx, finalKey)
	if objectMissing(err) {
		final, err = c.promote(ctx, stagingKey, finalKey, contentType, size)
	}
	if err != nil {
		return err
	}
	if !regularObject(final.ObjectType) || final.ContentLength != size || oss.ToString(final.ContentType) != contentType {
		return ErrInvalidMedia
	}
	if oss.ToString(final.ETag) == "" {
		return errors.New("OSS final object returned no ETag")
	}
	result, err := c.internal.GetObject(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(c.config.Bucket), Key: oss.Ptr(finalKey), IfMatch: final.ETag,
	})
	if err != nil {
		return redactOSSError("read final object", err)
	}
	defer result.Body.Close()
	if !regularObject(result.ObjectType) || result.ContentLength != size {
		return ErrInvalidMedia
	}
	return verifyMedia(ctx, c.config.ProbePath, kind, contentType, size, result.Body)
}

func (c *alibabaCloud) promote(ctx context.Context, sourceKey, finalKey, contentType string, size int64) (*oss.HeadObjectResult, error) {
	source, err := c.head(ctx, sourceKey)
	if err != nil {
		if objectMissing(err) {
			return nil, ErrObjectPending
		}
		return nil, err
	}
	if !regularObject(source.ObjectType) || source.ContentLength != size ||
		(oss.ToString(source.StorageClass) != "" && oss.ToString(source.StorageClass) != string(oss.StorageClassStandard)) {
		return nil, ErrInvalidMedia
	}
	if oss.ToString(source.ETag) == "" {
		return nil, errors.New("OSS staging object returned no ETag")
	}
	_, copyErr := c.internal.CopyObject(ctx, &oss.CopyObjectRequest{
		Bucket: oss.Ptr(c.config.Bucket), Key: oss.Ptr(finalKey),
		SourceBucket: oss.Ptr(c.config.Bucket), SourceKey: oss.Ptr(sourceKey),
		ForbidOverwrite: oss.Ptr("true"), IfMatch: source.ETag, MetadataDirective: oss.Ptr("REPLACE"),
		ContentType: oss.Ptr(contentType), ContentDisposition: oss.Ptr("inline"),
		StorageClass: oss.StorageClassStandard,
	})
	// Recheck the destination even if the copy response was lost. Only the
	// server can write the destination, and it never overwrites an existing key.
	final, err := c.head(ctx, finalKey)
	if err == nil {
		return final, nil
	}
	if copyErr != nil && objectMissing(err) {
		var check *oss.ServiceError
		if errors.As(copyErr, &check) {
			switch {
			case check.StatusCode == http.StatusPreconditionFailed || objectMissing(copyErr):
				return nil, ErrObjectPending
			case check.Code == "InvalidObjectState":
				return nil, ErrInvalidMedia
			}
		}
		return nil, redactOSSError("promote staging object", copyErr)
	}
	return nil, err
}

func (c *alibabaCloud) head(ctx context.Context, key string) (*oss.HeadObjectResult, error) {
	result, err := c.internal.HeadObject(ctx, &oss.HeadObjectRequest{
		Bucket: oss.Ptr(c.config.Bucket), Key: oss.Ptr(key),
	})
	if err != nil {
		if objectMissing(err) {
			return nil, err
		}
		return nil, redactOSSError("read object metadata", err)
	}
	return result, nil
}

func regularObject(objectType *string) bool {
	value := oss.ToString(objectType)
	return value == "Normal" || value == "Multipart"
}

func objectMissing(err error) bool {
	var result *oss.ServiceError
	return errors.As(err, &result) && result.StatusCode == http.StatusNotFound &&
		(result.Code == "NoSuchKey" || result.Code == "")
}

func redactOSSError(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var result *oss.ServiceError
	if errors.As(err, &result) {
		return fmt.Errorf("OSS %s returned HTTP %d", operation, result.StatusCode)
	}
	return fmt.Errorf("OSS %s failed", operation)
}

func (c *alibabaCloud) Sign(ctx context.Context, key, method string) (string, error) {
	if !objectPattern.MatchString(key) || !strings.HasPrefix(key, "direct-upload/assets/") {
		return "", errors.New("invalid playback key")
	}
	var request any
	switch method {
	case http.MethodGet:
		request = &oss.GetObjectRequest{Bucket: oss.Ptr(c.config.Bucket), Key: oss.Ptr(key)}
	case http.MethodHead:
		request = &oss.HeadObjectRequest{Bucket: oss.Ptr(c.config.Bucket), Key: oss.Ptr(key)}
	default:
		return "", errors.New("invalid playback method")
	}
	result, err := c.playback.Presign(ctx, request, oss.PresignExpires(15*time.Minute))
	if err != nil {
		return "", redactOSSError("sign playback", err)
	}
	return result.URL, nil
}

func (c *alibabaCloud) Delete(ctx context.Context, key string) error {
	if !objectPattern.MatchString(key) || !strings.HasPrefix(key, "direct-upload/assets/") {
		return errors.New("invalid cleanup key")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := c.internal.DeleteObject(ctx, &oss.DeleteObjectRequest{
		Bucket: oss.Ptr(c.config.Bucket), Key: oss.Ptr(key),
	})
	if err != nil && !objectMissing(err) {
		return redactOSSError("delete object", err)
	}
	return nil
}
