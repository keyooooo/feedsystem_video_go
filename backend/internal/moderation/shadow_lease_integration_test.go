//go:build moderation_e2e

package moderation

import (
	"errors"
	"os"
	"testing"
	"time"

	mysqlconfig "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestShadowLeaseRecoveryMySQL(t *testing.T) {
	dsn := os.Getenv("MODERATION_TEST_MYSQL_DSN")
	cfg, err := mysqlconfig.ParseDSN(dsn)
	if err != nil || cfg.DBName != "moderation_e2e_test" {
		t.Fatal("MODERATION_TEST_MYSQL_DSN must select moderation_e2e_test")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.AutoMigrate(&Case{}, &ShadowRun{}); err != nil {
		t.Fatal(err)
	}
	seed := Case{State: "queued", ReportID: uint(time.Now().UnixNano())}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Where("case_id = ?", seed.ID).Delete(&ShadowRun{}).Error; err != nil {
			t.Error(err)
		}
		if err := db.Delete(&seed).Error; err != nil {
			t.Error(err)
		}
	}()
	type claim struct {
		item Case
		err  error
	}
	claims := make(chan claim, 2)
	for range 2 {
		go func() { item, err := claimShadowCase(db); claims <- claim{item, err} }()
	}
	var old Case
	successes := 0
	for range 2 {
		result := <-claims
		if result.err == nil {
			successes++
			old = result.item
			continue
		}
		if !errors.Is(result.err, gorm.ErrRecordNotFound) {
			t.Fatal(result.err)
		}
	}
	if successes != 1 || old.ID != seed.ID || old.Attempts != 1 {
		t.Fatalf("expected single owner: claim=%+v successes=%d", old, successes)
	}
	expire := func() time.Time {
		t.Helper()
		cutoff := time.Now().UTC().Add(-2 * time.Second).Truncate(time.Millisecond)
		if err := db.Model(&seed).Update("lease_until", cutoff).Error; err != nil {
			t.Fatal(err)
		}
		return cutoff
	}
	secondReady := expire()
	current, err := claimShadowCase(db)
	if err != nil || current.ID != old.ID || current.Attempts != 2 || current.Version <= old.Version || !current.ReadyAt.Equal(secondReady) {
		t.Fatalf("lease takeover failed: %+v (%v)", current, err)
	}
	if err := finishShadowCase(db, old, ShadowRun{Failure: "stale_result"}, false, nil); err != nil {
		t.Fatal(err)
	}
	var state Case
	if err := db.First(&state, seed.ID).Error; err != nil || state.State != "running" || state.Version != current.Version {
		t.Fatalf("stale execution changed case: %+v (%v)", state, err)
	}
	expire()
	third, err := claimShadowCase(db)
	if err != nil || third.Attempts != 3 {
		t.Fatalf("expected final attempt: %+v (%v)", third, err)
	}
	expire()
	final, err := claimShadowCase(db)
	if err != nil || final.State != "awaiting_review" || final.Attempts != 3 || final.Version <= third.Version {
		t.Fatalf("exhausted case was not sent to manual review: %+v (%v)", final, err)
	}
	var attempts []ShadowRun
	if err := db.Where("case_id = ?", seed.ID).Order("attempt").Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 3 || attempts[0].Failure != "lease_expired" || attempts[2].ResultState != "awaiting_review" {
		t.Fatalf("unexpected lease history: %+v", attempts)
	}
}
