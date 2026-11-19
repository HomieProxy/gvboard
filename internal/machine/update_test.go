package machine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidVersion(t *testing.T) {
	for v, want := range map[string]bool{
		"v0.4.9-gvboard.3":      true,
		"v1.10.0-gvboard":       true,
		"v0.4.9-gvboard.2-rc1":  true,
		"v0.4.9":                false,
		"0.4.9-gvboard.3":       false,
		"v0.4.9-gvboard.3/../x": false,
		"v0.4.9-gvboard.3 ":     false,
		"latest":                false,
	} {
		if ValidVersion(v) != want {
			t.Errorf("ValidVersion(%q) 应为 %v", v, want)
		}
	}
}

func TestReleases(t *testing.T) {
	body := `[
		{"tag_name":"v0.4.9-gvboard.3","draft":false,"prerelease":true,"published_at":"2026-11-20T01:00:00Z","assets":[{"name":"v2node-linux-64.zip"}]},
		{"tag_name":"v0.4.9-gvboard.4","draft":true,"assets":[{"name":"v2node-linux-64.zip"}]},
		{"tag_name":"v0.4.9-gvboard.5","draft":false,"assets":[]},
		{"tag_name":"v0.4.9","draft":false,"assets":[{"name":"v2node-linux-64.zip"}]},
		{"tag_name":"v0.4.9-gvboard.2","draft":false,"prerelease":true,"published_at":"2026-11-19T01:00:00Z","assets":[{"name":"v2node-linux-64.zip.dgst"},{"name":"v2node-linux-64.zip"}]}
	]`
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	svc := &Service{ReleasesURL: srv.URL}
	list, err := svc.Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0] != (Release{Version: "v0.4.9-gvboard.3", PublishedAt: "2026-11-20T01:00:00Z", Prerelease: true}) || list[1].Version != "v0.4.9-gvboard.2" {
		t.Fatalf("版本列表不符（应排除草稿、未构建完成与非 gvboard 版本）: %+v", list)
	}
	status = http.StatusForbidden
	if _, err := svc.Releases(context.Background()); err == nil {
		t.Fatal("GitHub 返回错误时应失败")
	}
}
