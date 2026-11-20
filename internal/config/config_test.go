package config

import "testing"

// TestValidate pins the production BaseURL guard: an APP_ENV=production boot
// with the development default would poison every canonical, og:url, sitemap
// and llms.txt URL, so it must fail fast.
func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "production with localhost base url",
			cfg:     Config{AppEnv: "production", BaseURL: "http://localhost:8080"},
			wantErr: true,
		},
		{
			name:    "production with public base url",
			cfg:     Config{AppEnv: "production", BaseURL: "https://wangga.biz.id"},
			wantErr: false,
		},
		{
			name:    "development with localhost base url",
			cfg:     Config{AppEnv: "development", BaseURL: "http://localhost:8080"},
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.cfg)
			if tc.wantErr && err == nil {
				t.Fatal("Validate() = nil, want an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}
