package oauth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
	_ "time/tzdata"

	"golang.org/x/text/language"
)

// RequestEnvironment belongs to an OAuth account, not a provider display name
// or conversation. It never changes the host operating system.
type RequestEnvironment struct {
	DeviceID          string `json:"device_id"`
	Country           string `json:"country,omitempty"`
	Language          string `json:"language,omitempty"`
	Locale            string `json:"locale,omitempty"`
	Timezone          string `json:"timezone,omitempty"`
	DNS               string `json:"dns"`
	Source            string `json:"source,omitempty"`
	NeedsConfirmation bool   `json:"needs_confirmation,omitempty"`
}

type region struct {
	Country  string `json:"country"`
	Language string `json:"language"`
	Locale   string `json:"locale"`
}

// Language/locale suggestions derive from the configured request country.
// Timezones are chosen explicitly; exit and upstream metadata never set them.
var regions = []region{
	{"US", "en-US", "en-US"},
	{"CA", "en-CA", "en-CA"},
	{"GB", "en-GB", "en-GB"},
	{"AU", "en-AU", "en-AU"},
	{"SG", "en-SG", "en-SG"},
	{"DE", "de-DE", "de-DE"},
	{"FR", "fr-FR", "fr-FR"},
	{"NL", "nl-NL", "nl-NL"},
	{"JP", "ja-JP", "ja-JP"},
	{"KR", "ko-KR", "ko-KR"},
	{"HK", "zh-Hant-HK", "zh-Hant-HK"},
	{"TW", "zh-Hant-TW", "zh-Hant-TW"},
	{"CN", "zh-Hans-CN", "zh-Hans-CN"},
	{"IN", "en-IN", "en-IN"},
}

func (e *RequestEnvironment) Clone() *RequestEnvironment {
	if e == nil {
		return nil
	}
	copy := *e
	return &copy
}

func (e *RequestEnvironment) Complete() bool {
	return e != nil && e.Country != "" && e.Language != "" && e.Locale != "" && e.Timezone != ""
}

func AccountDeviceID(c *Credential) string {
	identity := normalizeIdentityString(c.AccountID)
	if identity == "" {
		identity = AccountIdentityKey(c)
	}
	if identity == "" {
		identity = "ref:" + strings.TrimSpace(c.Ref)
	}
	sum := sha256.Sum256([]byte("clipal-device-v1\x00" + string(normalizeProvider(c.Provider)) + "\x00" + identity))
	return hex.EncodeToString(sum[:])
}

// AccountEnvironmentIdentity guards environment edits without changing provider
// linking identities. Token-only legacy accounts use the stable device fallback.
func AccountEnvironmentIdentity(c *Credential) string {
	if c == nil {
		return ""
	}
	if identity := AccountIdentityKey(c); identity != "" {
		return identity
	}
	return "device:" + AccountDeviceID(c)
}

func NormalizeEnvironment(c *Credential, e *RequestEnvironment) (*RequestEnvironment, error) {
	if c == nil {
		return nil, fmt.Errorf("OAuth credential is required")
	}
	out := e.Clone()
	if out == nil {
		out = &RequestEnvironment{}
	}
	switch out.Source {
	case "default", "user":
	case "", "exit", "exit_inferred", "legacy":
		out.Source = ""
		out.NeedsConfirmation = true
	default:
		return nil, fmt.Errorf("environment source must be default or user")
	}
	out.DeviceID = AccountDeviceID(c)
	out.Country = strings.ToUpper(strings.TrimSpace(out.Country))
	if out.Country != "" && (len(out.Country) != 2 || out.Country[0] < 'A' || out.Country[0] > 'Z' || out.Country[1] < 'A' || out.Country[1] > 'Z') {
		return nil, fmt.Errorf("country must be a two-letter code")
	}
	for _, region := range regions {
		if out.Country == region.Country {
			if out.Source != "" && strings.TrimSpace(out.Language) == "" {
				out.Language = region.Language
			}
			if out.Source != "" && strings.TrimSpace(out.Locale) == "" {
				out.Locale = region.Locale
			}
		}
	}
	if out.Country != "" {
		if _, err := language.ParseRegion(out.Country); err != nil {
			return nil, fmt.Errorf("country must be a valid region code")
		}
		if out.Source != "" && out.Language == "" {
			tag := language.Make("und-" + out.Country)
			base, confidence := tag.Base()
			if confidence != language.No {
				out.Language = base.String() + "-" + out.Country
			}
		}
		if out.Source != "" && out.Locale == "" {
			out.Locale = out.Language
		}
	}
	for _, field := range []*string{&out.Language, &out.Locale} {
		*field = strings.TrimSpace(*field)
		if *field != "" {
			tag, err := language.Parse(*field)
			if err != nil || len(*field) > 80 {
				return nil, fmt.Errorf("language and locale must be valid BCP 47 tags")
			}
			*field = tag.String()
		}
	}
	out.Timezone = strings.TrimSpace(out.Timezone)
	if out.Timezone != "" {
		if _, err := time.LoadLocation(out.Timezone); err != nil || len(out.Timezone) > 100 || out.Timezone == "Local" {
			return nil, fmt.Errorf("timezone must be a valid IANA timezone")
		}
	}
	out.DNS = strings.ToLower(strings.TrimSpace(out.DNS))
	if out.DNS == "" {
		out.DNS = "auto"
	}
	if out.DNS != "auto" && out.DNS != "system" && out.DNS != "cloudflare" && out.DNS != "google" {
		return nil, fmt.Errorf("dns must be auto, system, cloudflare, or google")
	}
	return out, nil
}

func ApplyEnvironmentHeaders(r *http.Request, e *RequestEnvironment) {
	e = e.ForRequests()
	if r == nil || e == nil {
		return
	}
	if e.Language != "" {
		r.Header.Set("Accept-Language", e.Language)
	} else {
		r.Header.Del("Accept-Language")
	}
	// Only normalize existing environmental headers; do not invent upstream API fields.
	for name, value := range map[string]string{"X-Timezone": e.Timezone, "X-Client-Timezone": e.Timezone, "X-Locale": e.Locale, "X-Country-Code": e.Country} {
		if r.Header.Get(name) != "" {
			if value != "" {
				r.Header.Set(name, value)
			} else {
				r.Header.Del(name)
			}
		}
	}
}

// RegionOptions is selected when adding an account, independently of upstream
// claims or network observations. Defaults are request preferences, not domicile.
type RegionOptions struct {
	Country  string `json:"country"`
	Timezone string `json:"timezone"`
	Source   string `json:"source,omitempty"`
}

func NewAccountEnvironment(c *Credential, options *RegionOptions) (*RequestEnvironment, error) {
	selected := RegionOptions{Country: "US", Timezone: "America/New_York", Source: "default"}
	if options != nil {
		selected = *options
		if strings.TrimSpace(selected.Country) == "" || strings.TrimSpace(selected.Timezone) == "" {
			return nil, fmt.Errorf("request country and timezone are required")
		}
		if selected.Source == "" {
			selected.Source = "user"
		}
		if selected.Source != "default" && selected.Source != "user" {
			return nil, fmt.Errorf("environment source must be default or user")
		}
		if selected.Source == "default" && (strings.ToUpper(strings.TrimSpace(selected.Country)) != "US" || selected.Timezone != "America/New_York") {
			selected.Source = "user"
		}
	}
	return NormalizeEnvironment(c, &RequestEnvironment{Country: selected.Country, Timezone: selected.Timezone, Source: selected.Source})
}

// Unconfirmed legacy region values are retained for review, but not applied to
// outgoing account metadata. Device identity and the existing DNS policy remain.
func (e *RequestEnvironment) ForRequests() *RequestEnvironment {
	if e == nil {
		return nil
	}
	if !e.NeedsConfirmation {
		return e
	}
	out := e.Clone()
	out.Country = ""
	out.Language = ""
	out.Locale = ""
	out.Timezone = ""
	return out
}
