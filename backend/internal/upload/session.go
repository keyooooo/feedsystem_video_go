package upload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"time"

	"feedsystem_video_go/internal/account"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	Pending   = "pending"
	Ready     = "ready"
	Published = "published"
	Cancelled = "cancelled"
	Failed    = "failed"
	Expired   = "expired"

	Video  = "video"
	Cover  = "cover"
	Avatar = "avatar"

	sessionTTL = 24 * time.Hour
)

var (
	ErrRequest  = errors.New("invalid upload parameters")
	ErrNotFound = errors.New("upload not found")
	ErrConflict = errors.New("upload conflicts with its current state")
	ErrExpired  = errors.New("upload session expired")
	ErrLimit    = errors.New("upload quota or request rate exceeded")
)

// Session becomes an asset once the immutable final object passes validation.
type Session struct {
	ID          string     `gorm:"type:char(32);primaryKey" json:"id"`
	AccountID   uint       `gorm:"index:idx_upload_owner_created,priority:1;not null" json:"-"`
	Kind        string     `gorm:"size:8;not null" json:"kind"`
	Size        int64      `gorm:"not null" json:"size"`
	ContentType string     `gorm:"size:20;not null" json:"content_type"`
	StagingKey  string     `gorm:"size:128;not null" json:"-"`
	FinalKey    string     `gorm:"size:128;not null" json:"-"`
	Status      string     `gorm:"size:12;not null;index:idx_upload_cleanup,priority:1" json:"status"`
	CreatedAt   time.Time  `gorm:"index:idx_upload_owner_created,priority:2;not null" json:"-"`
	ExpiresAt   time.Time  `gorm:"index:idx_upload_cleanup,priority:2;not null" json:"expires_at"`
	PurgedAt    *time.Time `json:"-"`
	VideoID     uint       `json:"-"`
}

func (s Session) PublicPath() string {
	return "/api/media/assets/" + s.ID
}

func (s Session) State(now time.Time) string {
	if (s.Status == Pending || s.Status == Ready) && !s.ExpiresAt.After(now) {
		return Expired
	}
	return s.Status
}

type Service struct {
	db            *gorm.DB
	cloud         Cloud
	now           func() time.Time
	verifications chan struct{}
}

func NewService(db *gorm.DB, cloud Cloud) *Service {
	return &Service{db: db, cloud: cloud, now: time.Now, verifications: make(chan struct{}, 2)}
}

func (s *Service) Transaction(ctx context.Context, run func(*gorm.DB) error) error {
	return s.db.WithContext(ctx).Transaction(run)
}

func (s *Service) Create(ctx context.Context, owner uint, kind, contentType string, size int64) (Session, Grant, error) {
	var session Session
	var grant Grant
	if !validMediaDeclaration(kind, contentType, size) {
		return session, grant, ErrRequest
	}
	id, err := makeID()
	if err != nil {
		return session, grant, err
	}
	err = s.Transaction(ctx, func(tx *gorm.DB) error {
		var ownerRecord account.Account
		// Serialize quota decisions for the same account.
		if err := tx.Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).First(&ownerRecord, owner).Error; err != nil {
			return err
		}
		now := s.now().UTC().Truncate(time.Millisecond)
		var created int64
		if err := tx.Model(&Session{}).Where("account_id = ? AND created_at > ?", owner, now.Add(-time.Hour)).Count(&created).Error; err != nil {
			return err
		}
		if created >= 30 {
			return ErrLimit
		}
		var active struct{ Count, Bytes int64 }
		if err := tx.Model(&Session{}).Select("COUNT(*) AS count, COALESCE(SUM(size), 0) AS bytes").
			Where("account_id = ? AND status IN ? AND expires_at > ?", owner, []string{Pending, Ready}, now).Scan(&active).Error; err != nil {
			return err
		}
		if active.Count >= 12 || active.Bytes+size > 512<<20 {
			return ErrLimit
		}
		session = Session{
			ID: id, AccountID: owner, Kind: kind, Size: size, ContentType: contentType,
			StagingKey: "direct-upload/staging/" + kind + "/" + id,
			FinalKey:   "direct-upload/assets/" + kind + "/" + id,
			Status:     Pending, CreatedAt: now, ExpiresAt: now.Add(sessionTTL),
		}
		if err := tx.Create(&session).Error; err != nil {
			return err
		}
		grant, err = s.issueGrant(ctx, session)
		return err
	})
	return session, grant, err
}

func makeID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func findOwned(db *gorm.DB, owner uint, id string) (Session, error) {
	var session Session
	if !validID(id) {
		return session, ErrNotFound
	}
	err := db.Where("id = ? AND account_id = ?", id, owner).Take(&session).Error
	session.ExpiresAt = session.ExpiresAt.UTC()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = ErrNotFound
	}
	return session, err
}

func LockOwned(tx *gorm.DB, owner uint, id string) (Session, error) {
	return findOwned(tx.Clauses(clause.Locking{Strength: "UPDATE"}), owner, id)
}

func (s *Service) Status(ctx context.Context, owner uint, id string) (Session, error) {
	session, err := findOwned(s.db.WithContext(ctx), owner, id)
	if err == nil {
		session.Status = session.State(s.now())
	}
	return session, err
}

func (s *Service) issueGrant(ctx context.Context, session Session) (Grant, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	grant, err := s.cloud.Grant(ctx, session.StagingKey, session.Kind)
	if err == nil && !grant.ExpiresAt.After(s.now()) {
		return Grant{}, errors.New("STS grant expired")
	}
	if err == nil && grant.ExpiresAt.After(session.ExpiresAt) {
		return Grant{}, ErrExpired
	}
	return grant, err
}

func (s *Service) Renew(ctx context.Context, owner uint, id string) (Session, Grant, error) {
	var session Session
	var grant Grant
	err := s.Transaction(ctx, func(tx *gorm.DB) error {
		var err error
		session, err = LockOwned(tx, owner, id)
		if err != nil {
			return err
		}
		if session.State(s.now()) == Expired || session.ExpiresAt.Sub(s.now()) < grantDuration+10*time.Second {
			return ErrExpired
		}
		if session.Status != Pending {
			return ErrConflict
		}
		grant, err = s.issueGrant(ctx, session)
		return err
	})
	return session, grant, err
}

func (s *Service) Complete(ctx context.Context, owner uint, id string) (Session, error) {
	var session Session
	var mediaError error
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	err := s.Transaction(ctx, func(tx *gorm.DB) error {
		var err error
		session, err = LockOwned(tx, owner, id)
		if err != nil {
			return err
		}
		switch session.State(s.now()) {
		case Ready, Published:
			return nil
		case Failed:
			return ErrInvalidMedia
		case Expired:
			return ErrExpired
		case Pending:
		default:
			return ErrConflict
		}
		select {
		case s.verifications <- struct{}{}:
			defer func() { <-s.verifications }()
		default:
			return ErrLimit
		}
		err = s.cloud.Verify(ctx, session.StagingKey, session.FinalKey, session.Kind, session.ContentType, session.Size)
		if errors.Is(err, ErrInvalidMedia) {
			mediaError = ErrInvalidMedia
			session.Status = Failed
		} else if err != nil {
			return err
		} else {
			if !session.ExpiresAt.After(s.now()) {
				return ErrExpired
			}
			session.Status = Ready
		}
		return tx.Model(&session).Update("status", session.Status).Error
	})
	if err != nil {
		return session, err
	}
	return session, mediaError
}

func (s *Service) Cancel(ctx context.Context, owner uint, id string) (Session, error) {
	var session Session
	err := s.Transaction(ctx, func(tx *gorm.DB) error {
		var err error
		session, err = LockOwned(tx, owner, id)
		if err != nil {
			return err
		}
		switch session.State(s.now()) {
		case Cancelled:
			return nil
		case Pending, Ready:
			session.Status = Cancelled
			return tx.Model(&session).Update("status", Cancelled).Error
		case Expired:
			return ErrExpired
		default:
			return ErrConflict
		}
	})
	return session, err
}

func (s *Service) ApplyAvatar(ctx context.Context, owner uint, id string) (string, error) {
	var avatar string
	err := s.Transaction(ctx, func(tx *gorm.DB) error {
		session, err := LockOwned(tx, owner, id)
		if err != nil {
			return err
		}
		if session.Kind != Avatar {
			return ErrConflict
		}
		avatar = session.PublicPath()
		switch session.State(s.now()) {
		case Published:
			return nil
		case Ready:
			if err := tx.Model(&account.Account{}).Where("id = ?", owner).Update("avatar_url", avatar).Error; err != nil {
				return err
			}
			return tx.Model(&session).Update("status", Published).Error
		case Expired:
			return ErrExpired
		default:
			return ErrConflict
		}
	})
	return avatar, err
}

// Sweep reclaims finalized objects from expired or abandoned sessions. The
// bucket lifecycle separately removes staging objects and uncommitted parts.
func (s *Service) Sweep(ctx context.Context) error {
	var rows []Session
	now := s.now()
	if err := s.db.WithContext(ctx).
		Where("status IN ? AND expires_at <= ? AND purged_at IS NULL", []string{Pending, Ready, Failed, Cancelled}, now).
		Order("expires_at").Limit(50).Find(&rows).Error; err != nil {
		return err
	}
	var result error
	for _, row := range rows {
		err := s.Transaction(ctx, func(tx *gorm.DB) error {
			session, err := LockOwned(tx, row.AccountID, row.ID)
			if err != nil {
				return err
			}
			if session.Status == Published || session.PurgedAt != nil || session.ExpiresAt.After(now) {
				return nil
			}
			deleteCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := s.cloud.Delete(deleteCtx, session.FinalKey); err != nil {
				return err
			}
			return tx.Model(&session).Update("purged_at", now.UTC()).Error
		})
		result = errors.Join(result, err)
		if ctx.Err() != nil {
			break
		}
	}
	return result
}

func (s *Service) StartCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			sweepCtx, cancel := context.WithTimeout(ctx, time.Minute)
			err := s.Sweep(sweepCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Printf("OSS upload asset cleanup failed")
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()
}
