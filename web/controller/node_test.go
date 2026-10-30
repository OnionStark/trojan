package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMergeNodesKeepsPasswordAndRefreshesLocal(t *testing.T) {
	local := Node{ID: localNodeID, Name: "old", Server: "new.example", Port: 443, Sni: "new.example", Local: true}
	old := []Node{
		{ID: localNodeID, Name: "香港", Server: "old.example", Port: 80, Local: true},
		{ID: "a1b2c3d4", Name: "日本", Server: "jp.example", Port: 443, Sni: "jp.example", PanelURL: "https://jp.example", AdminPass: "hash", Local: false},
	}
	incoming := []Node{
		{ID: localNodeID, Name: "香港", Local: true},
		{ID: "a1b2c3d4", Name: "日本", Server: "jp.example", Port: 443, PanelURL: "https://jp.example", AdminPass: ""},
	}
	got, err := mergeNodes(local, old, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Server != "new.example" || got[0].Port != 443 || got[0].Name != "香港" {
		t.Fatalf("local node = %+v", got[0])
	}
	if got[1].AdminPass != "hash" || got[1].Sni != "jp.example" {
		t.Fatalf("remote node = %+v", got[1])
	}
}

func TestMergeNodesRequiresPasswordForNewRemote(t *testing.T) {
	local := Node{ID: localNodeID, Name: "local", Server: "a.example", Port: 443, Sni: "a.example", Local: true}
	_, err := mergeNodes(local, nil, []Node{
		{ID: localNodeID, Name: "本机", Local: true},
		{Name: "日本", Server: "jp.example", Port: 443, PanelURL: "https://jp.example"},
	})
	if err == nil || !strings.Contains(err.Error(), "管理员密码") {
		t.Fatalf("err=%v", err)
	}
}

func TestMergeNodesRejectsDuplicateName(t *testing.T) {
	local := Node{ID: localNodeID, Name: "local", Server: "a.example", Port: 443, Local: true}
	_, err := mergeNodes(local, nil, []Node{
		{ID: localNodeID, Name: "同名", Local: true},
		{Name: "同名", Server: "b.example", Port: 443, PanelURL: "https://b.example", AdminPass: "hash"},
	})
	if err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("err=%v", err)
	}
}

func TestApplyAssignmentCreateUpdateDelete(t *testing.T) {
	nodes := []Node{
		{ID: localNodeID, Name: "本机", Server: "local.example", Port: 443, Local: true},
		{ID: "jp", Name: "日本", Server: "jp.example", Port: 443},
		{ID: "hk", Name: "香港", Server: "hk.example", Port: 443},
	}
	fake := &fakeSyncer{}
	results, applied := applyAssignment(nodes, "alice", "alice", "cGFzcw==", []string{localNodeID, "jp"}, []string{localNodeID, "hk"}, fake)
	if len(results) != 2 {
		t.Fatalf("results=%+v", results)
	}
	if !strings.Contains(strings.Join(fake.ensured, ","), "hk") {
		t.Fatalf("ensured=%v", fake.ensured)
	}
	if !strings.Contains(strings.Join(fake.removed, ","), "jp") {
		t.Fatalf("removed=%v", fake.removed)
	}
	if strings.Join(applied, ",") != "hk,"+localNodeID {
		t.Fatalf("applied=%v", applied)
	}
}

func TestApplyAssignmentKeepsNodeWhenDeleteFails(t *testing.T) {
	nodes := []Node{
		{ID: localNodeID, Name: "本机", Local: true, Server: "a.example", Port: 443},
		{ID: "jp", Name: "日本", Server: "jp.example", Port: 443},
	}
	fake := &fakeSyncer{failRemove: true}
	results, applied := applyAssignment(nodes, "alice", "alice", "cGFzcw==", []string{localNodeID, "jp"}, []string{localNodeID}, fake)
	if len(results) != 1 || results[0].OK {
		t.Fatalf("results=%+v", results)
	}
	if strings.Join(applied, ",") != "jp,"+localNodeID {
		t.Fatalf("applied=%v", applied)
	}
}

func TestBuildClashConfigSelectsAllNodes(t *testing.T) {
	nodes := []Node{
		{ID: localNodeID, Name: "香港", Server: "hk.example", Port: 443, Sni: "hk.example", Local: true},
		{Name: "日本", Server: "jp.example", Port: 8443, Sni: "jp.example"},
	}
	got := BuildClashConfig(nodes, "p:ass", ", network: ws")
	if !strings.Contains(got, `type: select`) {
		t.Fatalf("missing select: %s", got)
	}
	if !strings.Contains(got, `"香港"`) || !strings.Contains(got, `"日本"`) {
		t.Fatalf("missing names: %s", got)
	}
	if !strings.Contains(got, `"p:ass"`) {
		t.Fatalf("password not quoted: %s", got)
	}
	if strings.Count(got, "network: ws") != 1 {
		t.Fatalf("websocket should only apply to local node: %s", got)
	}
	if !strings.Contains(got, "port: 8443") {
		t.Fatalf("remote port missing: %s", got)
	}
	if strings.Index(got, `"香港"`) > strings.Index(got, `"日本"`) {
		t.Fatalf("local node should be the default: %s", got)
	}
}

func TestBuildClashConfigPutsLocalFirst(t *testing.T) {
	nodes := []Node{
		{Name: "us", Server: "us.example", Port: 443, Sni: "us.example"},
		{ID: localNodeID, Name: "本机", Server: "local.example", Port: 443, Sni: "local.example", Local: true},
	}
	got := BuildClashConfig(nodes, "pass", "")
	if strings.Index(got, `"本机"`) > strings.Index(got, `"us"`) {
		t.Fatalf("local node should be listed first: %s", got)
	}
}

func TestRemoteEnsureAndRemove(t *testing.T) {
	var users []map[string]interface{}
	var restarted int
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["username"] != "admin" || body["password"] != "hash" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"message": "bad"})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"code": 200, "token": "tok"})
	})
	mux.HandleFunc("/trojan/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(map[string]interface{}{
				"Msg":  "success",
				"Data": map[string]interface{}{"userList": users},
			})
		case http.MethodPost:
			r.ParseForm()
			users = append(users, map[string]interface{}{
				"ID":       7,
				"Username": r.Form.Get("username"),
			})
			json.NewEncoder(w).Encode(map[string]string{"Msg": "success"})
		case http.MethodDelete:
			users = nil
			json.NewEncoder(w).Encode(map[string]string{"Msg": "success"})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/trojan/user/update", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if len(users) == 0 || r.Form.Get("id") != "7" {
			json.NewEncoder(w).Encode(map[string]string{"Msg": "missing"})
			return
		}
		users[0]["Username"] = r.Form.Get("username")
		json.NewEncoder(w).Encode(map[string]string{"Msg": "success"})
	})
	mux.HandleFunc("/trojan/restart", func(w http.ResponseWriter, r *http.Request) {
		restarted++
		json.NewEncoder(w).Encode(map[string]string{"Msg": "success"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := newRemoteClient()
	node := Node{PanelURL: srv.URL, AdminPass: "hash", Name: "日本"}
	if err := client.Ensure(node, "alice", "alice", "cGFzcw=="); err != nil {
		t.Fatal(err)
	}
	if restarted != 0 {
		t.Fatalf("create should not restart, restarted=%d", restarted)
	}
	if err := client.Ensure(node, "alice", "alice2", "cGFzcw=="); err != nil {
		t.Fatal(err)
	}
	if restarted != 0 {
		t.Fatalf("update should not restart trojan, restarted=%d", restarted)
	}
	if users[0]["Username"] != "alice2" {
		t.Fatalf("username=%v", users[0]["Username"])
	}
	if err := client.Remove(node, "alice2"); err != nil {
		t.Fatal(err)
	}
	if len(users) != 0 {
		t.Fatalf("users still present: %+v", users)
	}
	if err := client.Remove(node, "alice2"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteEnsureUpdateSurvivesRestartEOF(t *testing.T) {
	users := []map[string]interface{}{{"ID": uint(7), "Username": "alice"}}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"code": 200, "token": "tok"})
	})
	mux.HandleFunc("/trojan/user", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"Msg":  "success",
			"Data": map[string]interface{}{"userList": users},
		})
	})
	mux.HandleFunc("/trojan/user/update", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"Msg": "success"})
	})
	mux.HandleFunc("/trojan/restart", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("sync should not restart trojan")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := newRemoteClient()
	node := Node{PanelURL: srv.URL, AdminPass: "hash", Name: "美国"}
	if err := client.Ensure(node, "alice", "alice", "cGFzcw=="); err != nil {
		t.Fatal(err)
	}
}

func TestApplyAssignmentKeepsNodeWhenSyncFails(t *testing.T) {
	nodes := []Node{
		{ID: localNodeID, Name: "本机", Local: true},
		{ID: "us", Name: "美国"},
	}
	results, applied := applyAssignment(nodes, "alice", "alice", "cGFzcw==", []string{localNodeID}, []string{localNodeID, "us"}, timeoutSyncer{})
	if len(results) != 1 || results[0].OK {
		t.Fatalf("results=%+v", results)
	}
	if strings.Join(applied, ",") != localNodeID+",us" {
		t.Fatalf("applied=%v", applied)
	}
}

type timeoutSyncer struct{}

func (timeoutSyncer) Ensure(node Node, oldName, newName, passB64 string) error {
	return errors.New("登录失败: timeout")
}

func (timeoutSyncer) Remove(node Node, username string) error {
	return nil
}

func TestApplyAssignmentKeepsNodeWhenAccountUpdated(t *testing.T) {
	nodes := []Node{
		{ID: localNodeID, Name: "本机", Local: true},
		{ID: "us", Name: "美国"},
	}
	syncer := failAfterUpdate{}
	results, applied := applyAssignment(nodes, "alice", "alice", "cGFzcw==", []string{localNodeID}, []string{localNodeID, "us"}, syncer)
	if len(results) != 1 || results[0].OK || !strings.Contains(results[0].Msg, "账号已更新") {
		t.Fatalf("results=%+v", results)
	}
	if strings.Join(applied, ",") != localNodeID+",us" {
		t.Fatalf("applied=%v", applied)
	}
}

type failAfterUpdate struct{}

func (failAfterUpdate) Ensure(node Node, oldName, newName, passB64 string) error {
	return errors.New("账号已更新，重启失败: EOF")
}

func (failAfterUpdate) Remove(node Node, username string) error {
	return nil
}

type fakeSyncer struct {
	ensured    []string
	removed    []string
	failRemove bool
}

func (f *fakeSyncer) Ensure(node Node, oldName, newName, passB64 string) error {
	if passB64 == "" {
		return errors.New("empty pass")
	}
	f.ensured = append(f.ensured, node.ID+":"+oldName+">"+newName)
	return nil
}

func (f *fakeSyncer) Remove(node Node, username string) error {
	if f.failRemove {
		return errors.New("down")
	}
	f.removed = append(f.removed, node.ID+":"+username)
	return nil
}

func TestRemovedRemoteIgnoresLocal(t *testing.T) {
	old := []Node{{ID: localNodeID, Local: true, Name: "本机"}, {ID: "jp", Name: "日本"}}
	next := []Node{{ID: localNodeID, Local: true, Name: "本机"}}
	got := removedRemote(old, next)
	if len(got) != 1 || got[0].ID != "jp" {
		t.Fatalf("removed=%+v", got)
	}
}

func TestParseNodeIDs(t *testing.T) {
	got := parseNodeIDs(" local, jp, local, ")
	if strings.Join(got, ",") != "local,jp" {
		t.Fatalf("got=%v", got)
	}
}
