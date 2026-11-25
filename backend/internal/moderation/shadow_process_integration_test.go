//go:build moderation_e2e

package moderation

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	mysqlconfig "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const shadowHelperEnv = "MODERATION_E2E_SHADOW_CHILD"

func TestShadowWorkerKilledMySQL(t *testing.T) {
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

	if os.Getenv(shadowHelperEnv) == "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		err := RunShadow(ctx, db, func(attempt context.Context, report Report) (ShadowRun, bool) {
			fmt.Printf("lease-held:%d\n", report.ID)
			<-attempt.Done()
			return ShadowRun{Failure: "child_timed_out"}, false
		}, nil)
		t.Fatalf("lease child exited without being terminated: %v", err)
	}
	if err := db.AutoMigrate(&Case{}, &ShadowRun{}, &Report{}); err != nil {
		t.Fatal(err)
	}
	report := Report{ReporterID: 1, CommentID: 1, RequestID: fmt.Sprintf("worker-kill-%d", time.Now().UnixNano()), ReasonCode: "other"}
	if err := db.Create(&report).Error; err != nil {
		t.Fatal(err)
	}
	seed := Case{State: "queued", ReportID: report.ID}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, err := range []error{db.Where("case_id = ?", seed.ID).Delete(&ShadowRun{}).Error, db.Delete(&seed).Error, db.Delete(&report).Error} {
			if err != nil {
				t.Error(err)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run", "^TestShadowWorkerKilledMySQL$")
	child.Env = append(os.Environ(), shadowHelperEnv+"=1")
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		expected := fmt.Sprintf("lease-held:%d", report.ID)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == expected {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case held := <-ready:
		if !held {
			t.Fatal("child did not claim the test report")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for independent worker to claim report")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	var killed *exec.ExitError
	err = child.Wait()
	waited = true
	if !errors.As(err, &killed) {
		t.Fatalf("expected forced process exit, got %v", err)
	}
	if _, err := claimShadowCase(db); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("lease was reusable before expiry: %v", err)
	}
	// Advance only the isolated row's lease to keep CI independent of the production TTL.
	if err := db.Model(&seed).Update("lease_until", time.Now().UTC().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	reclaimed, err := claimShadowCase(db)
	if err != nil || reclaimed.ID != seed.ID || reclaimed.Attempts != 2 {
		t.Fatalf("reclaim failed: %+v (%v)", reclaimed, err)
	}
	if err := finishShadowCase(db, reclaimed, ShadowRun{Failure: "manual_fixture"}, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&seed, seed.ID).Error; err != nil || seed.State != "awaiting_review" {
		t.Fatalf("reclaimed case did not finish: %+v (%v)", seed, err)
	}
	var history []ShadowRun
	if err := db.Where("case_id = ?", seed.ID).Order("attempt").Find(&history).Error; err != nil || len(history) != 2 ||
		history[0].Failure != "lease_expired" || history[1].Failure != "manual_fixture" {
		t.Fatalf("unexpected history: %+v (%v)", history, err)
	}
}
