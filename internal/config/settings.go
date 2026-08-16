package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Persisted runtime settings. These are stored in the settings table and
// applied on top of environment defaults at boot. POCKETPDS_SECRET is
// intentionally excluded (it must come from the environment).
const (
	SettingPublicURL      = "public_url"
	SettingServiceDID     = "service_did"
	SettingDIDMethod      = "did_method"
	SettingInviteRequired = "invite_required"
	SettingAdminToken     = "admin_token"
	SettingSMTPHost       = "smtp_host"
	SettingSMTPPort       = "smtp_port"
	SettingSMTPUser       = "smtp_user"
	SettingSMTPPass       = "smtp_pass"
	SettingSMTPFrom       = "smtp_from"
)

// ApplySettings overlays persisted settings onto the config. Unknown keys are
// ignored. Values are trusted to have been validated at write time, but a
// minimal guard is kept for did_method.
func (c *Config) ApplySettings(overrides map[string]string) {
	for k, v := range overrides {
		switch k {
		case SettingPublicURL:
			if v != "" {
				c.PublicURL = v
			}
		case SettingServiceDID:
			c.ServiceDID = v
		case SettingDIDMethod:
			if v == "web" || v == "plc" {
				c.DIDMethod = v
			}
		case SettingInviteRequired:
			c.InviteRequired = v == "true"
		case SettingAdminToken:
			c.AdminToken = v
		case SettingSMTPHost:
			c.SMTPHost = v
		case SettingSMTPPort:
			if v != "" {
				c.SMTPPort = v
			}
		case SettingSMTPUser:
			c.SMTPUser = v
		case SettingSMTPPass:
			c.SMTPPass = v
		case SettingSMTPFrom:
			c.SMTPFrom = v
		}
	}
}

// ValidateSetting returns an error if a persisted setting value is invalid.
func ValidateSetting(key, value string) error {
	switch key {
	case SettingPublicURL:
		u, err := url.Parse(value)
		if err != nil || u.Host == "" {
			return fmt.Errorf("public_url must be a valid URL")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("public_url must use http or https")
		}
	case SettingServiceDID:
		if value != "" && !strings.HasPrefix(value, "did:") {
			return fmt.Errorf("service_did must be a DID or empty")
		}
	case SettingDIDMethod:
		if value != "web" && value != "plc" {
			return fmt.Errorf("did_method must be \"web\" or \"plc\"")
		}
	case SettingInviteRequired:
		if value != "true" && value != "false" {
			return fmt.Errorf("invite_required must be true or false")
		}
	case SettingAdminToken, SettingSMTPHost, SettingSMTPUser, SettingSMTPPass, SettingSMTPFrom:
		// any string is acceptable
	case SettingSMTPPort:
		if value != "" {
			if _, err := strconv.Atoi(value); err != nil {
				return fmt.Errorf("smtp_port must be a number")
			}
		}
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
	return nil
}
