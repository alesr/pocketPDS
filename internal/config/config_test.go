package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEffectiveServiceDID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "explicit service did wins",
			cfg:  Config{ServiceDID: "did:plc:explicit", PublicURL: "https://pds.example.com", DIDMethod: "web"},
			want: "did:plc:explicit",
		},
		{
			name: "derives from https url",
			cfg:  Config{PublicURL: "https://pds.example.com", DIDMethod: "web"},
			want: "did:web:pds.example.com",
		},
		{
			name: "derives with non-default port",
			cfg:  Config{PublicURL: "http://127.0.0.1:3000", DIDMethod: "web"},
			want: "did:web:127.0.0.1%3A3000",
		},
		{
			name: "omits default http port",
			cfg:  Config{PublicURL: "http://example.com:80", DIDMethod: "web"},
			want: "did:web:example.com",
		},
		{
			name: "empty for plc method",
			cfg:  Config{PublicURL: "https://pds.example.com", DIDMethod: "plc"},
			want: "",
		},
		{
			name: "empty for invalid url",
			cfg:  Config{PublicURL: "not-a-url", DIDMethod: "web"},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.cfg.EffectiveServiceDID())
		})
	}
}
