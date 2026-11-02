package httpapi_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"healthbeat-server/internal/agentpkg"
	"healthbeat-server/internal/settings"
)

type agentPackageView struct {
	ID               string  `json:"id"`
	Version          string  `json:"version"`
	Filename         string  `json:"filename"`
	OriginalFilename string  `json:"original_filename"`
	Arch             string  `json:"arch"`
	Format           string  `json:"format"`
	SizeBytes        int64   `json:"size_bytes"`
	SHA256           string  `json:"sha256"`
	UploadedByEmail  string  `json:"uploaded_by_email"`
	DownloadCount    int64   `json:"download_count"`
	LastDownloadedAt *string `json:"last_downloaded_at"`
}

type agentPackageList struct {
	StorageAvailable bool               `json:"storage_available"`
	DownloadPath     string             `json:"download_path"`
	Packages         []agentPackageView `json:"packages"`
}

type uploadError struct {
	Error  string            `json:"error"`
	Fields map[string]string `json:"fields"`
}

// withAgentPackages, API'ye geçici bir paket dizini bağlar ve yolunu döndürür.
func (a *api) withAgentPackages() string {
	a.t.Helper()
	root := filepath.Join(a.t.TempDir(), "agent-packages")
	dir, err := agentpkg.NewDir(root)
	if err != nil {
		a.t.Fatal(err)
	}
	a.deps.SetAgentPackagesDir(dir)
	return root
}

// uploadAgentPackage, tek bir dosyayı yükler. fields sırayla ad, değer çiftleridir ve dosyadan önce yazılır; filename
// boşsa dosya parçası gönderilmez.
func (a *api) uploadAgentPackage(token, filename, body string, fields ...string) (int, agentPackageView, uploadError) {
	a.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for i := 0; i+1 < len(fields); i += 2 {
		_ = mw.WriteField(fields[i], fields[i+1])
	}
	if filename != "" {
		part, err := mw.CreateFormFile("file", filename)
		if err != nil {
			a.t.Fatal(err)
		}
		_, _ = part.Write([]byte(body))
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/api/v1/agent-packages", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	var pkg agentPackageView
	var fail uploadError
	target := any(&fail)
	if rec.Code == http.StatusCreated {
		target = &pkg
	}
	if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
		a.t.Fatalf("decode %d %q: %v", rec.Code, rec.Body.String(), err)
	}
	return rec.Code, pkg, fail
}

// upload, dosyayı sürüm ve mimariyle yükler.
func (a *api) upload(token, filename, body, ver, arch string) (int, agentPackageView, uploadError) {
	a.t.Helper()
	return a.uploadAgentPackage(token, filename, body, "version", ver, "arch", arch)
}

// download, indirme ucunu kimlik doğrulamasız çağırır.
func (a *api) download(method, path string, headers ...string) *httptest.ResponseRecorder {
	a.t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

const (
	debName = "healthbeat-agent_1.1.0_amd64.deb"
	debBody = "!<arch>\ndeb paketi"
	rpmName = "healthbeat-agent-1.1.0-1.aarch64.rpm"
	rpmBody = "\xed\xab\xee\xdbrpm paketi"
	rcName  = "healthbeat-agent_1.2.0~rc.1_amd64.deb"
)

func TestAgentPackageEndpointsNeedPermission(t *testing.T) {
	a := newAPI(t)
	a.withAgentPackages()
	orgAdmin, _ := a.login("oa@x.test", "org_admin")
	operator, _ := a.login("op@x.test", "operator")
	for _, tok := range []string{orgAdmin, operator} {
		a.expect(403, "GET", "/api/v1/agent-packages", tok, nil, nil)
		if code, _, _ := a.upload(tok, debName, debBody, "1.1.0", "amd64"); code != 403 {
			t.Errorf("upload = %d, want 403", code)
		}
	}
	a.expect(401, "GET", "/api/v1/agent-packages", "", nil, nil)
}

func TestAgentPackageUploadListDownloadDelete(t *testing.T) {
	a := newAPI(t)
	root := a.withAgentPackages()
	tok, _ := a.login("root@x.test", "super_admin")

	// Ad ne olursa olsun sürüm ve mimari formdan, biçim içerikten gelir; dosya release adıyla saklanır.
	sum := sha256.Sum256([]byte(debBody))
	code, p, fail := a.upload(tok, "agent-yeni.deb", debBody, "1.1.0", "amd64")
	if code != 201 || p.Filename != debName || p.OriginalFilename != "agent-yeni.deb" || p.Version != "1.1.0" ||
		p.Arch != "amd64" || p.Format != "deb" || p.SizeBytes != int64(len(debBody)) || p.SHA256 != hex.EncodeToString(sum[:]) ||
		p.UploadedByEmail != "root@x.test" {
		t.Fatalf("deb upload = %d %+v %+v", code, p, fail)
	}
	if code, p, fail := a.upload(tok, `C:\fakepath\paket.bin`, rpmBody, "1.1.0", "arm64"); code != 201 || p.Filename != rpmName ||
		p.Format != "rpm" || p.OriginalFilename != "paket.bin" {
		t.Fatalf("rpm upload = %d %+v %+v", code, p, fail)
	}
	if code, p, fail := a.upload(tok, rcName, debBody, "1.2.0-rc.1", "amd64"); code != 201 || p.Filename != rcName {
		t.Fatalf("prerelease upload = %d %+v %+v", code, p, fail)
	}
	if got := a.auditCount("agent_package.upload"); got != 3 {
		t.Fatalf("upload audit entries = %d, want 3", got)
	}
	if b, err := os.ReadFile(filepath.Join(root, "1.1.0", debName)); err != nil || string(b) != debBody {
		t.Fatalf("stored file = %q, %v", b, err)
	}

	// Aynı sürüm, mimari ve biçim ikinci kez yüklenmez; dosya değişmez.
	if code, _, fail := a.upload(tok, "baska.deb", "!<arch>\nbaşka içerik", "1.1.0", "amd64"); code != 409 || fail.Error == "" {
		t.Fatalf("duplicate upload = %d %+v", code, fail)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "1.1.0", debName)); string(b) != debBody {
		t.Fatalf("duplicate upload replaced the file: %q", b)
	}

	var list agentPackageList
	a.expect(200, "GET", "/api/v1/agent-packages", tok, nil, &list)
	if !list.StorageAvailable || list.DownloadPath != "/agent/download/" || len(list.Packages) != 3 ||
		list.Packages[0].Filename != rcName || list.Packages[1].Filename != rpmName || list.Packages[2].Filename != debName {
		t.Fatalf("list = %+v", list)
	}
	deb := list.Packages[2]

	// İndirme: kimlik doğrulamasız; tam indirme ve baştan başlayan Range sayılır, sürdürme, HEAD ve 304 sayılmaz.
	path := "/agent/download/1.1.0/" + debName
	rec := a.download("GET", path, "User-Agent", "curl/8.5.0")
	if rec.Code != 200 || rec.Body.String() != debBody || rec.Header().Get("Content-Type") != "application/octet-stream" ||
		rec.Header().Get("ETag") != `"`+deb.SHA256+`"` || rec.Header().Get("Content-Disposition") != `attachment; filename="`+debName+`"` {
		t.Fatalf("download = %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	if rec := a.download("GET", path, "Range", "bytes=0-3"); rec.Code != 206 || rec.Body.String() != debBody[:4] {
		t.Fatalf("range from the start = %d %q", rec.Code, rec.Body.String())
	}
	if rec := a.download("GET", path, "Range", "bytes=4-"); rec.Code != 206 || rec.Body.String() != debBody[4:] {
		t.Fatalf("resumed range = %d %q", rec.Code, rec.Body.String())
	}
	if rec := a.download("HEAD", path); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("HEAD = %d, body %d bytes", rec.Code, rec.Body.Len())
	}
	if rec := a.download("GET", path, "If-None-Match", `"`+deb.SHA256+`"`); rec.Code != 304 {
		t.Fatalf("conditional GET with the current ETag = %d, want 304", rec.Code)
	}
	a.expect(200, "GET", "/api/v1/agent-packages", tok, nil, &list)
	if got := list.Packages[2]; got.DownloadCount != 2 || got.LastDownloadedAt == nil {
		t.Fatalf("after downloads: %+v, want 2 (full and range from 0)", got)
	}
	var downloads struct {
		Downloads []struct {
			ClientIP  string `json:"client_ip"`
			UserAgent string `json:"user_agent"`
		} `json:"downloads"`
	}
	a.expect(200, "GET", "/api/v1/agent-packages/"+deb.ID+"/downloads?limit=10", tok, nil, &downloads)
	if len(downloads.Downloads) != 2 || downloads.Downloads[1].UserAgent != "curl/8.5.0" || downloads.Downloads[1].ClientIP == "" {
		t.Fatalf("downloads = %+v", downloads)
	}
	a.expect(400, "GET", "/api/v1/agent-packages/"+deb.ID+"/downloads?limit=0", tok, nil, nil)
	a.expect(404, "GET", "/api/v1/agent-packages/00000000-0000-0000-0000-000000000000/downloads", tok, nil, nil)

	// Yanlış sürüm, tanınmayan ad ve olmayan paket aynı 404'ü alır.
	for _, p := range []string{
		"/agent/download/1.2.0/" + debName,
		"/agent/download/1.1.0/SHA256SUMS",
		"/agent/download/1.1.0/healthbeat-agent_1.1.0_arm64.deb",
		"/agent/download/1.1.0/..%2F..%2Fetc%2Fpasswd",
	} {
		if rec := a.download("GET", p); rec.Code != 404 {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
	}

	// Ayardan kapatılınca indirme 404; yönetim çalışmaya devam eder.
	if _, err := a.settings.Update(t.Context(), nil, settings.Patch{AgentDownloadEnabled: new(bool)}); err != nil {
		t.Fatal(err)
	}
	if rec := a.download("GET", path); rec.Code != 404 {
		t.Fatalf("download while turned off = %d, want 404", rec.Code)
	}
	a.expect(200, "GET", "/api/v1/agent-packages", tok, nil, nil)

	// Silme: dosya ve kayıt gider; ikinci silme 404.
	a.expect(204, "DELETE", "/api/v1/agent-packages/"+deb.ID, tok, nil, nil)
	if _, err := os.Stat(filepath.Join(root, "1.1.0", debName)); !os.IsNotExist(err) {
		t.Fatalf("file left after delete: %v", err)
	}
	a.expect(404, "DELETE", "/api/v1/agent-packages/"+deb.ID, tok, nil, nil)
	if got := a.auditCount("agent_package.delete"); got != 1 {
		t.Fatalf("delete audit entries = %d, want 1", got)
	}
}

func TestAgentPackageUploadRejectsBadRequests(t *testing.T) {
	a := newAPI(t)
	tok, _ := a.login("root@x.test", "super_admin")

	// Dizin yoksa yükleme 503, liste "kullanılamıyor" der; indirme 404.
	if code, _, _ := a.upload(tok, debName, debBody, "1.1.0", "amd64"); code != http.StatusServiceUnavailable {
		t.Fatalf("upload without a directory = %d, want 503", code)
	}
	var list agentPackageList
	a.expect(200, "GET", "/api/v1/agent-packages", tok, nil, &list)
	if list.StorageAvailable || list.Packages == nil {
		t.Fatalf("list without a directory = %+v", list)
	}
	if rec := a.download("GET", "/agent/download/1.1.0/"+debName); rec.Code != 404 {
		t.Fatalf("download without a directory = %d", rec.Code)
	}

	root := a.withAgentPackages()
	a.expect(400, "POST", "/api/v1/agent-packages", tok, map[string]string{"a": "b"}, nil) // JSON, multipart değil
	if code, _, _ := a.uploadAgentPackage(tok, "", "", "version", "1.1.0", "arch", "amd64"); code != 400 {
		t.Fatalf("upload with no file = %d, want 400", code)
	}
	for _, c := range []struct {
		name, body, field string
		fields            []string
	}{
		{"missing version", debBody, "version", []string{"arch", "amd64"}},
		{"bad version", debBody, "version", []string{"version", "1.1", "arch", "amd64"}},
		{"dash in prerelease", debBody, "version", []string{"version", "1.2.0-rc-1", "arch", "amd64"}},
		{"missing arch", debBody, "arch", []string{"version", "1.1.0"}},
		{"bad arch", debBody, "arch", []string{"version", "1.1.0", "arch", "x86_64"}},
	} {
		code, _, fail := a.uploadAgentPackage(tok, "agent.deb", c.body, c.fields...)
		if code != 400 || fail.Fields[c.field] == "" {
			t.Errorf("%s: %d %+v, want 400 on %s", c.name, code, fail, c.field)
		}
	}
	if code, _, fail := a.upload(tok, "SHA256SUMS", "özet", "1.1.0", "amd64"); code != 400 || fail.Error == "" {
		t.Errorf("unknown content = %d %+v, want 400", code, fail)
	}
	if code, _, fail := a.upload(tok, "agent.deb", "", "1.1.0", "amd64"); code != 400 || fail.Error != "dosya boş" {
		t.Errorf("empty file = %d %+v", code, fail)
	}
	// Alanlar dosyadan sonra gelirse dosya sürümsüz kalır.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "agent.deb")
	_, _ = part.Write([]byte(debBody))
	_ = mw.WriteField("version", "1.1.0")
	_ = mw.WriteField("arch", "amd64")
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/api/v1/agent-packages", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("fields after the file = %d, want 400", rec.Code)
	}
	// Reddedilen yüklemeler diskte bir şey bırakmaz.
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("entries left after rejected uploads: %v", entries)
	}
}

func TestAgentDownloadIsRateLimited(t *testing.T) {
	a := newAPI(t)
	a.withAgentPackages()
	tok, _ := a.login("root@x.test", "super_admin")
	if code, _, fail := a.upload(tok, "agent.deb", debBody, "1.2.0-rc.1", "amd64"); code != 201 {
		t.Fatalf("upload = %d %+v", code, fail)
	}
	// Önsürümün adresi: sürüm '-', .deb adı '~' ile.
	if rec := a.download("GET", "/agent/download/1.2.0-rc.1/"+rcName); rec.Code != 200 || rec.Body.String() != debBody {
		t.Fatalf("prerelease download = %d %q", rec.Code, rec.Body.String())
	}
	limited := 0
	for range 15 {
		rec := a.download("GET", "/agent/download/1.2.0-rc.1/"+rcName)
		if rec.Code == http.StatusTooManyRequests {
			limited++
			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("429 without Retry-After")
			}
		}
	}
	if limited == 0 {
		t.Fatal("15 downloads in a row from one IP were never limited")
	}
}
