package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *pgxpool.Pool {
	host := os.Getenv("APP_DB_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("APP_DB_PORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("APP_DB_USER")
	if user == "" {
		user = "user"
	}
	pass := os.Getenv("APP_DB_PASS")
	if pass == "" {
		pass = "pass"
	}
	adminConnStr := fmt.Sprintf("postgres://%s:%s@%s:%s/postgres?application_name=esp8266-web-test-admin",
		user, pass, host, port)
	adminConfig, err := pgxpool.ParseConfig(adminConnStr)
	require.NoError(t, err)
	adminPool, err := pgxpool.NewWithConfig(context.Background(), adminConfig)
	require.NoError(t, err)
	defer adminPool.Close()

	testDBName := fmt.Sprintf("esp8266_test_%d", time.Now().UnixNano())
	_, err = adminPool.Exec(context.Background(), "CREATE DATABASE "+testDBName)
	require.NoError(t, err)

	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?application_name=esp8266-web-test",
		user, pass, host, port, testDBName)
	config, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err)
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Close()
		adminPool2, err := pgxpool.NewWithConfig(context.Background(), adminConfig)
		if err == nil {
			defer adminPool2.Close()
			_, _ = adminPool2.Exec(context.Background(), "DROP DATABASE IF EXISTS "+testDBName)
		}
	})
	return pool
}

func TestApplyMigrations(t *testing.T) {
	db := setupTestDB(t)
	app := &app{db: db, secretKey: "dummy"}
	err := app.applyMigrations(context.Background())
	assert.NoError(t, err)
	var exists bool
	err = db.QueryRow(context.Background(), "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'readings')").Scan(&exists)
	assert.NoError(t, err)
	assert.True(t, exists)
}

func TestDataHandlerPOST(t *testing.T) {
	db := setupTestDB(t)
	app := &app{db: db, secretKey: "testsecret"}
	require.NoError(t, app.applyMigrations(context.Background()))

	body := `{"tempCo": 25.5, "tempRoom": 22.0, "timestamp": 1761388101}`
	req := httptest.NewRequest("POST", "/data", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Secret-Key", "testsecret")
	w := httptest.NewRecorder()

	app.dataHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp TemperatureReading
	err := json.NewDecoder(w.Body).Decode(&resp)

	assert.NoError(t, err)
	assert.NotNil(t, resp.Id)
	assert.Equal(t, 25.5, resp.TempCo)
	assert.Equal(t, 22.0, resp.TempRoom)
	assert.Equal(t, int64(1761388101), *resp.Timestamp)
}

func TestDataHandlerPOSTNilTimestamp(t *testing.T) {
	db := setupTestDB(t)
	app := &app{db: db, secretKey: "testsecret"}
	require.NoError(t, app.applyMigrations(context.Background()))

	body := `{"tempCo": 26.0, "tempRoom": 23.0}`
	req := httptest.NewRequest("POST", "/data", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Secret-Key", "testsecret")
	w := httptest.NewRecorder()

	app.dataHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp TemperatureReading
	err := json.NewDecoder(w.Body).Decode(&resp)

	assert.NoError(t, err)
	assert.NotNil(t, resp.Id)
	assert.Equal(t, 26.0, resp.TempCo)
	assert.Equal(t, 23.0, resp.TempRoom)
	assert.NotNil(t, resp.Timestamp)
}

func TestDataHandlerGET(t *testing.T) {
	db := setupTestDB(t)
	app := &app{db: db, secretKey: "dummy"}
	require.NoError(t, app.applyMigrations(context.Background()))

	_, err := db.Exec(context.Background(), "INSERT INTO readings (temp_co, temp_room, timestamp) VALUES ($1, $2, $3)", 27.0, 24.0, time.Now().UTC().Unix())
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/data", nil)
	w := httptest.NewRecorder()

	app.dataHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []TemperatureReading
	err = json.NewDecoder(w.Body).Decode(&resp)

	assert.NoError(t, err)
	assert.Greater(t, len(resp), 0)
}

func TestDataHandlerGETEmpty(t *testing.T) {
	db := setupTestDB(t)
	app := &app{db: db, secretKey: "dummy"}
	require.NoError(t, app.applyMigrations(context.Background()))

	_, err := db.Exec(context.Background(), "DELETE FROM readings")
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/data", nil)
	w := httptest.NewRecorder()

	app.dataHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []TemperatureReading
	err = json.NewDecoder(w.Body).Decode(&resp)

	assert.NoError(t, err)
	assert.Equal(t, 0, len(resp))
}

func TestDataHandlerPOSTInvalidAuth(t *testing.T) {
	db := setupTestDB(t)
	app := &app{db: db, secretKey: "testsecret"}
	require.NoError(t, app.applyMigrations(context.Background()))

	body := `{"tempCo": 25.5, "tempRoom": 22.0}`
	req := httptest.NewRequest("POST", "/data", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Secret-Key", "wrongkey")
	w := httptest.NewRecorder()

	app.dataHandler(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDataHandlerInvalidMethod(t *testing.T) {
	app := &app{}
	req := httptest.NewRequest("PUT", "/data", nil)
	w := httptest.NewRecorder()

	app.dataHandler(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestHealthHandler(t *testing.T) {
	app := &app{}
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	app.healthHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"status": "ok"}`, w.Body.String())
}

func TestHomeHandler(t *testing.T) {
	app := &app{}
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	app.homeHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Temperature Monitor")
}
