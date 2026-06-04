package serviceurl

import (
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestBase(t *testing.T) {
	tests := []struct {
		server  string
		service string
		want    string
		wantErr bool
	}{
		{"https://pazer.build", "sites", "https://sites.pazer.build", false},
		{"https://pazer.build", "dl", "https://dl.pazer.build", false},
		{"https://buildhost.example.com", "sites", "https://sites.buildhost.example.com", false},
		{"http://localhost:8080", "sites", "http://sites.localhost:8080", false},
		{"https://pazer.build/", "sites", "https://sites.pazer.build", false},
		// error cases: missing scheme or host
		{"pazer.build", "sites", "", true},
		{"", "sites", "", true},
		{"https://", "sites", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.server+"/"+tt.service, func(t *testing.T) {
			got, err := Base(tt.server, tt.service)
			if tt.wantErr {
				require.NotNil(t, err)
				return
			}
			require.Nil(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
