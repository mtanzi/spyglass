// Package pgtest gives tests a real Postgres. It is test support: only
// _test.go files import it.
//
// The server is the one named by SPYGLASS_TEST_DATABASE_URL when that is set,
// otherwise a Postgres 16 container started once per test binary with
// testcontainers-go. Without either, tests that need it skip. Each test gets
// its own empty database, dropped when the test ends.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// EnvURL names the variable that points the tests at an existing server. The
// URL's user must be allowed to create databases.
const EnvURL = "SPYGLASS_TEST_DATABASE_URL"

// Image is the container image used when EnvURL is not set: the oldest
// supported Postgres.
const Image = "postgres:16"

var (
	once      sync.Once
	serverURL string
	skipMsg   string
	startErr  error
	container *tcpostgres.PostgresContainer
)

// Main runs the tests of a package that uses NewDatabase and then stops the
// container, if one was started. Call it from TestMain.
func Main(m *testing.M) {
	code := m.Run()
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
	os.Exit(code)
}

// NewDatabase creates an empty database and returns its URL. The database is
// dropped when t ends. It skips t when no server is available.
func NewDatabase(t testing.TB) string {
	t.Helper()
	once.Do(start)
	if skipMsg != "" {
		t.Skip(skipMsg)
	}
	if startErr != nil {
		t.Fatalf("pgtest: %v", startErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := pgx.Connect(ctx, serverURL)
	if err != nil {
		t.Fatalf("pgtest: connect: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	var b [8]byte
	_, _ = rand.Read(b[:])
	name := "spyglass_test_" + hex.EncodeToString(b[:])
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		t.Fatalf("pgtest: create database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		conn, err := pgx.Connect(ctx, serverURL)
		if err != nil {
			t.Errorf("pgtest: drop database: %v", err)
			return
		}
		defer func() { _ = conn.Close(ctx) }()
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
			t.Errorf("pgtest: drop database: %v", err)
		}
	})

	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("pgtest: %s is not a URL", EnvURL)
	}
	u.Path = "/" + name
	return u.String()
}

func start() {
	if v := os.Getenv(EnvURL); v != "" {
		serverURL = v
		return
	}
	if err := dockerHealth(); err != nil {
		skipMsg = fmt.Sprintf("pgtest: no Postgres: set %s or start Docker (%v)", EnvURL, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	container = c
	if err != nil {
		startErr = fmt.Errorf("start %s: %w", Image, err)
		return
	}
	serverURL, startErr = c.ConnectionString(ctx, "sslmode=disable")
}

// dockerHealth reports whether testcontainers can reach a Docker daemon.
// testcontainers can panic when it finds no Docker host at all.
func dockerHealth() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	provider, err := testcontainers.ProviderDocker.GetProvider()
	if err != nil {
		return err
	}
	defer func() { _ = provider.Close() }()
	return provider.Health(context.Background())
}
