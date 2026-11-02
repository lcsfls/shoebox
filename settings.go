package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

type AISettings struct {
	Enabled    bool   `json:"enabled"`
	BaseURL    string `json:"base_url"`    // OpenAI-compatible, e.g. http://gpu-box:8000/v1 (vLLM, Ollama, llama.cpp)
	APIKey     string `json:"api_key"`     // write-only through the API
	Model      string `json:"model"`       // e.g. the Baidu OCR model as served by vLLM
	Fields     bool   `json:"fields"`      // also ask the model for title, date, sender, amount
	Prompt     string `json:"prompt"`      // OCR prompt; empty = default
	TimeoutSec int    `json:"timeout_sec"` // per page
	MaxPages   int    `json:"max_pages"`   // pages per document sent to the model
}

type Settings struct {
	Pattern      string      `json:"pattern"`       // file name pattern inside the archive
	Languages    string      `json:"languages"`     // Tesseract languages, e.g. deu+eng
	Workers      int         `json:"workers"`       // documents processed at the same time
	WatchMinutes int         `json:"watch_minutes"` // look for new files in the archive every N minutes, 0 = off
	WatchOCR     bool        `json:"watch_ocr"`     // add a text layer to PDFs found in the archive that have none
	Learn        bool        `json:"learn"`         // suggest tags and sender from similar filed documents
	BackupDaily  bool        `json:"backup_daily"`  // copy the database into the archive folder once a day
	BackupKeep   int         `json:"backup_keep"`   // how many backups to keep
	StampLang    string      `json:"stamp_lang"`    // de | en: the stamps offered first
	StampIn      string      `json:"stamp_in"`      // German label of the "received" stamp
	StampPaid    string      `json:"stamp_paid"`    // German label of the "paid" stamp
	StampInEn    string      `json:"stamp_in_en"`   // English labels
	StampPaidEn  string      `json:"stamp_paid_en"`
	UpdateAuto   bool        `json:"update_auto"` // install new releases by themselves
	UpdateHour   int         `json:"update_hour"` // at this hour of the night
	AI           AISettings  `json:"ai"`
	Metrics      MetricsPush `json:"metrics"` // send the statistics to a server
}

func defaultSettings() Settings {
	return Settings{
		Pattern:      "{year}/{month}/{date}_{sender}_{title}",
		Languages:    "deu+eng",
		Workers:      1,
		WatchMinutes: 5,
		Metrics:      MetricsPush{Format: "json", Minutes: 5},
		WatchOCR:     true,
		Learn:        true,
		BackupKeep:   14,
		UpdateHour:   3,
		StampLang:    "de",
		StampIn:      "Eingang",
		StampPaid:    "Bezahlt",
		StampInEn:    "Received",
		StampPaidEn:  "Paid",
		AI:           AISettings{TimeoutSec: 180, MaxPages: 5},
	}
}

var settingsMu sync.Mutex

func (a *App) settings() Settings {
	s := defaultSettings()
	var raw string
	if err := a.db.QueryRow(`SELECT value FROM settings WHERE key = 'app'`).Scan(&raw); err == nil {
		json.Unmarshal([]byte(raw), &s)
	}
	return s
}

func (a *App) saveSettings(s Settings) error {
	b, _ := json.Marshal(s)
	_, err := a.db.Exec(`INSERT INTO settings(key, value) VALUES('app', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(b))
	return err
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	s := a.settings()
	hasKey := s.AI.APIKey != ""
	s.AI.APIKey = ""
	// the export's header holds a password or token: write-only as well
	hasHeader := s.Metrics.Header != ""
	s.Metrics.Header = ""
	writeJSON(w, 200, map[string]any{"settings": s, "ai_key_set": hasKey, "metrics_header_set": hasHeader, "languages": tesseractLanguages(), "default_prompt": defaultOCRPrompt})
}

func (a *App) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Settings
		ClearKey    bool `json:"clear_key"`
		ClearHeader bool `json:"clear_metrics_header"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	settingsMu.Lock()
	defer settingsMu.Unlock()
	old := a.settings()
	s := in.Settings
	s.Pattern = strings.Trim(clean(s.Pattern, 200), "/ ")
	if s.Pattern == "" || !strings.Contains(s.Pattern, "{") {
		s.Pattern = defaultSettings().Pattern
	}
	if strings.Contains(s.Pattern, "..") {
		fail(w, 400, "The pattern can't contain “..”.")
		return
	}
	s.Languages = clean(s.Languages, 60)
	if s.Languages == "" {
		s.Languages = "eng"
	}
	s.Workers = min(max(s.Workers, 1), 4)
	s.WatchMinutes = min(max(s.WatchMinutes, 0), 1440)
	s.AI.BaseURL = strings.TrimRight(clean(s.AI.BaseURL, 300), "/")
	s.AI.Model = clean(s.AI.Model, 200)
	s.AI.Prompt = clean(s.AI.Prompt, 4000)
	s.AI.TimeoutSec = min(max(s.AI.TimeoutSec, 10), 1800)
	s.AI.MaxPages = min(max(s.AI.MaxPages, 1), 50)
	// The key is never sent to the browser: keep the stored one unless a new one was typed.
	if strings.TrimSpace(s.AI.APIKey) == "" {
		s.AI.APIKey = old.AI.APIKey
	}
	if in.ClearKey {
		s.AI.APIKey = ""
	}
	s.Metrics.URL = clean(s.Metrics.URL, 500)
	s.Metrics.Minutes = min(max(s.Metrics.Minutes, 1), 1440)
	if s.Metrics.Format != "influx" && s.Metrics.Format != "prometheus" {
		s.Metrics.Format = "json"
	}
	s.Metrics.Header = clean(s.Metrics.Header, 1000)
	if s.Metrics.Header == "" {
		s.Metrics.Header = old.Metrics.Header
	}
	if in.ClearHeader {
		s.Metrics.Header = ""
	}
	if err := a.saveSettings(s); err != nil {
		fail(w, 500, err.Error())
		return
	}
	a.watcher.kick()
	writeJSON(w, 200, map[string]any{"ok": true})
}
