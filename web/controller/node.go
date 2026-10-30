package controller

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/tidwall/gjson"
	"trojan/core"
	"trojan/trojan"
)

const (
	localNodeID  = "local"
	nodesKey     = "trojan_nodes"
	userNodesKey = "user_node_map"
)

// Node 一条可分享、可同步账号的线路。
// Server/Port 是客户端连接用的地址，PanelURL 是对方网页管理的根地址。
type Node struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Server    string `json:"server"`
	Port      int    `json:"port"`
	Sni       string `json:"sni"`
	PanelURL  string `json:"panelUrl"`
	AdminPass string `json:"adminPass"`
	Local     bool   `json:"local"`
}

// NodePublic 返回给页面的节点，不包含管理员密码。
type NodePublic struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Server      string `json:"server"`
	Port        int    `json:"port"`
	Sni         string `json:"sni"`
	PanelURL    string `json:"panelUrl,omitempty"`
	Local       bool   `json:"local"`
	HasPassword bool   `json:"hasPassword,omitempty"`
}

// SyncResult 某个节点上创建、修改或删除账号的结果。
type SyncResult struct {
	NodeID string `json:"nodeId"`
	Name   string `json:"name"`
	Action string `json:"action"`
	OK     bool   `json:"ok"`
	Msg    string `json:"msg"`
}

// nodeSyncer 把账号变更发到其他服务器。
type nodeSyncer interface {
	Ensure(node Node, oldName, newName, passB64 string) error
	Remove(node Node, username string) error
}

func isNotFound(err error) bool {
	return err != nil && errors.Is(err, leveldb.ErrNotFound)
}

func currentLocalNode() Node {
	domain, port := trojan.GetDomainAndPort()
	name := domain
	if name == "" {
		name = "local"
	}
	return Node{
		ID:     localNodeID,
		Name:   name,
		Server: domain,
		Port:   port,
		Sni:    domain,
		Local:  true,
	}
}

func loadStoredNodes() ([]Node, error) {
	raw, err := core.GetValue(nodesKey)
	if isNotFound(err) || raw == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var nodes []Node
	if err := json.Unmarshal([]byte(raw), &nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

func storeNodes(nodes []Node) error {
	data, err := json.Marshal(nodes)
	if err != nil {
		return err
	}
	return core.SetValue(nodesKey, string(data))
}

func refreshLocal(nodes []Node) []Node {
	local := currentLocalNode()
	if len(nodes) == 0 {
		return []Node{local}
	}
	found := false
	for i := range nodes {
		if nodes[i].Local || nodes[i].ID == localNodeID {
			name := nodes[i].Name
			if strings.TrimSpace(name) == "" {
				name = local.Name
			}
			nodes[i].ID = localNodeID
			nodes[i].Local = true
			nodes[i].Name = name
			nodes[i].Server = local.Server
			nodes[i].Port = local.Port
			nodes[i].Sni = local.Sni
			nodes[i].PanelURL = ""
			nodes[i].AdminPass = ""
			found = true
		}
	}
	if !found {
		nodes = append([]Node{local}, nodes...)
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Local != nodes[j].Local {
			return nodes[i].Local
		}
		return nodes[i].Name < nodes[j].Name
	})
	return nodes
}

// LoadNodes 读取节点。没有配置时返回当前服务器这一条。
func LoadNodes() []Node {
	stored, err := loadStoredNodes()
	if err != nil || len(stored) == 0 {
		return []Node{currentLocalNode()}
	}
	return refreshLocal(stored)
}

func toPublic(nodes []Node, includePanel bool) []NodePublic {
	out := make([]NodePublic, 0, len(nodes))
	for _, n := range nodes {
		item := NodePublic{
			ID:     n.ID,
			Name:   n.Name,
			Server: n.Server,
			Port:   n.Port,
			Sni:    n.Sni,
			Local:  n.Local,
		}
		if includePanel {
			item.PanelURL = n.PanelURL
			item.HasPassword = n.AdminPass != ""
		}
		out = append(out, item)
	}
	return out
}

// GetNodes 管理员读取节点列表。
func GetNodes() *ResponseBody {
	responseBody := ResponseBody{Msg: "success"}
	responseBody.Data = map[string]interface{}{
		"nodes": toPublic(LoadNodes(), true),
	}
	return &responseBody
}

func newNodeID(used map[string]bool) string {
	for {
		buf := make([]byte, 4)
		if _, err := rand.Read(buf); err != nil {
			return hex.EncodeToString([]byte(fmt.Sprintf("%d", len(used)+1)))
		}
		id := hex.EncodeToString(buf)
		if id != localNodeID && !used[id] {
			return id
		}
	}
}

func validatePanel(panel string) error {
	u, err := url.Parse(panel)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("面板地址无效")
	}
	return nil
}

// mergeNodes 用本次提交覆盖节点列表，并沿用未修改的管理员密码。
func mergeNodes(local Node, oldNodes, incoming []Node) ([]Node, error) {
	oldByID := make(map[string]Node, len(oldNodes))
	for _, n := range oldNodes {
		oldByID[n.ID] = n
	}
	used := map[string]bool{}
	names := map[string]bool{}
	result := make([]Node, 0, len(incoming)+1)
	hasLocal := false
	for _, n := range incoming {
		n.Name = strings.TrimSpace(n.Name)
		n.Server = strings.TrimSpace(n.Server)
		n.Sni = strings.TrimSpace(n.Sni)
		n.PanelURL = strings.TrimRight(strings.TrimSpace(n.PanelURL), "/")
		n.AdminPass = strings.TrimSpace(n.AdminPass)
		if n.Name == "" {
			return nil, errors.New("节点名称不能为空")
		}
		if names[n.Name] {
			return nil, fmt.Errorf("节点名称重复: %s", n.Name)
		}
		names[n.Name] = true
		if n.Local || n.ID == localNodeID {
			if hasLocal {
				return nil, errors.New("只能有一个本机节点")
			}
			if strings.TrimSpace(local.Name) == "" {
				local.Name = n.Name
			}
			n.ID = localNodeID
			n.Local = true
			n.Name = n.Name
			n.Server = local.Server
			n.Port = local.Port
			n.Sni = local.Sni
			n.PanelURL = ""
			n.AdminPass = ""
			hasLocal = true
			used[n.ID] = true
			result = append(result, n)
			continue
		}
		if n.Server == "" {
			return nil, fmt.Errorf("节点 %s 缺少域名", n.Name)
		}
		if n.Port <= 0 || n.Port > 65535 {
			return nil, fmt.Errorf("节点 %s 端口无效", n.Name)
		}
		if strings.ContainsAny(n.Server, " /") {
			return nil, fmt.Errorf("节点 %s 域名无效", n.Name)
		}
		if n.Sni == "" {
			n.Sni = n.Server
		}
		if err := validatePanel(n.PanelURL); err != nil {
			return nil, fmt.Errorf("节点 %s 的面板地址无效", n.Name)
		}
		if n.ID == "" || n.ID == localNodeID {
			n.ID = newNodeID(used)
		}
		if used[n.ID] {
			return nil, fmt.Errorf("节点编号重复: %s", n.Name)
		}
		if n.AdminPass == "" {
			old, ok := oldByID[n.ID]
			if !ok || old.AdminPass == "" {
				return nil, fmt.Errorf("节点 %s 需要填写管理员密码", n.Name)
			}
			n.AdminPass = old.AdminPass
		}
		n.Local = false
		used[n.ID] = true
		result = append(result, n)
	}
	if !hasLocal {
		if old, ok := oldByID[localNodeID]; ok && strings.TrimSpace(old.Name) != "" {
			local.Name = old.Name
		}
		if names[local.Name] {
			return nil, fmt.Errorf("节点名称重复: %s", local.Name)
		}
		result = append([]Node{local}, result...)
	}
	return result, nil
}

func removedRemote(oldNodes, newNodes []Node) []Node {
	keep := map[string]bool{}
	for _, n := range newNodes {
		keep[n.ID] = true
	}
	var removed []Node
	for _, n := range oldNodes {
		if n.Local || n.ID == localNodeID || n.ID == "" {
			continue
		}
		if !keep[n.ID] {
			removed = append(removed, n)
		}
	}
	return removed
}

func loadUserNodeMap() (map[string][]string, error) {
	raw, err := core.GetValue(userNodesKey)
	if isNotFound(err) || raw == "" {
		return map[string][]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string][]string{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func storeUserNodeMap(m map[string][]string) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return core.SetValue(userNodesKey, string(data))
}

func userNodeIDs(username string) ([]string, bool, error) {
	m, err := loadUserNodeMap()
	if err != nil {
		return nil, false, err
	}
	ids, ok := m[username]
	return ids, ok, nil
}

func saveAssignment(oldName, newName string, ids []string) error {
	m, err := loadUserNodeMap()
	if err != nil {
		return err
	}
	if oldName != "" && oldName != newName {
		delete(m, oldName)
	}
	copied := append([]string{}, ids...)
	sort.Strings(copied)
	m[newName] = copied
	return storeUserNodeMap(m)
}

func deleteAssignment(username string) error {
	m, err := loadUserNodeMap()
	if err != nil {
		return err
	}
	delete(m, username)
	return storeUserNodeMap(m)
}

func usernamesOnNode(nodeID string) ([]string, error) {
	m, err := loadUserNodeMap()
	if err != nil {
		return nil, err
	}
	var names []string
	for name, ids := range m {
		for _, id := range ids {
			if id == nodeID {
				names = append(names, name)
				break
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func stripNode(nodeID string) error {
	m, err := loadUserNodeMap()
	if err != nil {
		return err
	}
	for name, ids := range m {
		next := make([]string, 0, len(ids))
		for _, id := range ids {
			if id != nodeID {
				next = append(next, id)
			}
		}
		m[name] = next
	}
	return storeUserNodeMap(m)
}

func fillNodeIDs(users []*core.User) {
	m, err := loadUserNodeMap()
	if err != nil {
		return
	}
	for _, user := range users {
		if user == nil {
			continue
		}
		if ids, ok := m[user.Username]; ok {
			user.NodeIDs = ids
		}
	}
}

func parseNodeIDs(raw string) []string {
	seen := map[string]bool{}
	var ids []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		ids = append(ids, part)
	}
	return ids
}

func containsID(ids []string, id string) bool {
	for _, item := range ids {
		if item == id {
			return true
		}
	}
	return false
}

// applyAssignment 按新旧节点差集创建、修改或删除远程账号，并返回实际生效的节点。
func applyAssignment(nodes []Node, oldName, newName, passB64 string, oldIDs, newIDs []string, syncer nodeSyncer) ([]SyncResult, []string) {
	byID := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	applied := map[string]bool{}
	for _, id := range oldIDs {
		if _, ok := byID[id]; ok {
			applied[id] = true
		}
	}
	var results []SyncResult
	for _, id := range newIDs {
		node, ok := byID[id]
		if !ok {
			results = append(results, SyncResult{NodeID: id, Name: id, Action: "create", OK: false, Msg: "节点不存在"})
			continue
		}
		if node.Local {
			applied[id] = true
			continue
		}
		action := "create"
		if containsID(oldIDs, id) {
			action = "update"
		}
		err := syncer.Ensure(node, oldName, newName, passB64)
		res := SyncResult{NodeID: node.ID, Name: node.Name, Action: action, OK: err == nil}
		if err != nil {
			res.Msg = err.Error()
			if action == "create" {
				delete(applied, id)
			}
		} else {
			applied[id] = true
		}
		results = append(results, res)
	}
	for _, id := range oldIDs {
		if containsID(newIDs, id) {
			continue
		}
		node, ok := byID[id]
		if !ok || node.Local {
			delete(applied, id)
			continue
		}
		err := syncer.Remove(node, oldName)
		res := SyncResult{NodeID: node.ID, Name: node.Name, Action: "delete", OK: err == nil}
		if err != nil {
			res.Msg = err.Error()
			applied[id] = true
		} else {
			delete(applied, id)
		}
		results = append(results, res)
	}
	ids := make([]string, 0, len(applied))
	for id := range applied {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return results, ids
}

// SaveNodes 保存节点。被移除的远程节点会删除已分配到该节点的账号。
func SaveNodes(c *gin.Context) *ResponseBody {
	responseBody := ResponseBody{Msg: "success"}
	var req struct {
		Nodes []Node `json:"nodes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		responseBody.Msg = "节点数据格式错误"
		return &responseBody
	}
	old, err := loadStoredNodes()
	if err != nil {
		responseBody.Msg = err.Error()
		return &responseBody
	}
	merged, err := mergeNodes(currentLocalNode(), old, req.Nodes)
	if err != nil {
		responseBody.Msg = err.Error()
		return &responseBody
	}
	keep := append([]Node{}, merged...)
	var results []SyncResult
	for _, node := range removedRemote(old, merged) {
		names, err := usernamesOnNode(node.ID)
		if err != nil {
			responseBody.Msg = err.Error()
			return &responseBody
		}
		okAll := true
		for _, name := range names {
			err := defaultSyncer.Remove(node, name)
			res := SyncResult{NodeID: node.ID, Name: node.Name, Action: "delete", OK: err == nil}
			if err != nil {
				res.Msg = name + ": " + err.Error()
				okAll = false
			}
			results = append(results, res)
		}
		if okAll {
			if err := stripNode(node.ID); err != nil {
				results = append(results, SyncResult{NodeID: node.ID, Name: node.Name, Action: "delete", OK: false, Msg: err.Error()})
				okAll = false
			}
		}
		if !okAll {
			keep = append(keep, node)
		}
	}
	if err := storeNodes(refreshLocal(keep)); err != nil {
		responseBody.Msg = err.Error()
		return &responseBody
	}
	responseBody.Data = map[string]interface{}{
		"nodes": toPublic(LoadNodes(), true),
		"sync":  results,
	}
	return &responseBody
}

func syncUser(oldName, newName, passB64, rawIDs string, legacyLocal bool) ([]SyncResult, error) {
	ids := parseNodeIDs(rawIDs)
	if len(ids) == 0 {
		ids = []string{localNodeID}
	}
	var oldIDs []string
	if legacyLocal {
		oldIDs = []string{localNodeID}
	}
	results, applied := applyAssignment(LoadNodes(), oldName, newName, passB64, oldIDs, ids, defaultSyncer)
	if err := saveAssignment(oldName, newName, applied); err != nil {
		return results, err
	}
	return results, nil
}

// CreateUserAndSync 在本机创建用户，并按勾选在其他服务器创建相同账号。
func CreateUserAndSync(username, password, rawIDs string) *ResponseBody {
	body := CreateUser(username, password)
	if body.Msg != "success" {
		return body
	}
	results, err := syncUser(username, username, password, rawIDs, false)
	if err != nil {
		body.Msg = err.Error()
		return body
	}
	body.Data = map[string]interface{}{"sync": results}
	return body
}

// UpdateUserAndSync 修改本机用户，并把用户名、密码和节点分配同步到其他服务器。
func UpdateUserAndSync(id uint, username, password, rawIDs string) *ResponseBody {
	mysql := core.GetMysql()
	userList, err := mysql.GetData(strconv.Itoa(int(id)))
	if err != nil {
		return &ResponseBody{Msg: err.Error()}
	}
	if len(userList) == 0 {
		return &ResponseBody{Msg: "用户不存在"}
	}
	oldName := userList[0].Username
	_, existed, err := userNodeIDs(oldName)
	if err != nil {
		return &ResponseBody{Msg: err.Error()}
	}
	body := UpdateUser(id, username, password)
	if body.Msg != "success" {
		return body
	}
	oldIDs := []string{}
	legacy := !existed
	if existed {
		oldIDs, _, _ = userNodeIDs(oldName)
		legacy = false
	}
	ids := parseNodeIDs(rawIDs)
	if len(ids) == 0 {
		ids = []string{localNodeID}
	}
	var useOld []string
	if legacy {
		useOld = []string{localNodeID}
	} else {
		useOld = oldIDs
	}
	results, applied := applyAssignment(LoadNodes(), oldName, username, password, useOld, ids, defaultSyncer)
	if err := saveAssignment(oldName, username, applied); err != nil {
		body.Msg = err.Error()
		return body
	}
	body.Data = map[string]interface{}{"sync": results}
	return body
}

// DelUserAndSync 删除本机用户，并删除其在其他服务器上的账号。远程删除失败时保留本机用户。
func DelUserAndSync(id uint) *ResponseBody {
	mysql := core.GetMysql()
	userList, err := mysql.GetData(strconv.Itoa(int(id)))
	if err != nil {
		return &ResponseBody{Msg: err.Error()}
	}
	if len(userList) == 0 {
		return &ResponseBody{Msg: "用户不存在"}
	}
	name := userList[0].Username
	ids, existed, err := userNodeIDs(name)
	if err != nil {
		return &ResponseBody{Msg: err.Error()}
	}
	if !existed {
		if err := deleteAssignment(name); err != nil {
			return &ResponseBody{Msg: err.Error()}
		}
		return DelUser(id)
	}
	results, applied := applyAssignment(LoadNodes(), name, name, "", ids, []string{localNodeID}, defaultSyncer)
	failed := false
	for _, item := range results {
		if !item.OK {
			failed = true
			break
		}
	}
	if failed {
		_ = saveAssignment(name, name, applied)
		return &ResponseBody{Msg: syncFailText(results), Data: map[string]interface{}{"sync": results}}
	}
	body := DelUser(id)
	if body.Msg != "success" {
		return body
	}
	if err := deleteAssignment(name); err != nil {
		body.Msg = err.Error()
		return body
	}
	body.Data = map[string]interface{}{"sync": results}
	return body
}

func syncFailText(results []SyncResult) string {
	parts := []string{"部分节点同步失败"}
	for _, item := range results {
		if !item.OK {
			parts = append(parts, item.Name+": "+item.Msg)
		}
	}
	return strings.Join(parts, "; ")
}

func yamlQuote(value string) string {
	return strconv.Quote(value)
}

func localWebsocketSuffix() string {
	configData := string(core.Load(""))
	if !gjson.Get(configData, "websocket").Exists() || !gjson.Get(configData, "websocket.enabled").Bool() {
		return ""
	}
	wsHost := ""
	if gjson.Get(configData, "websocket.host").Exists() {
		hostTemp := gjson.Get(configData, "websocket.host").String()
		if hostTemp != "" {
			wsHost = fmt.Sprintf(", headers: {Host: %s}", hostTemp)
		}
	}
	wsOpt := fmt.Sprintf("{path: %s%s}", gjson.Get(configData, "websocket.path").String(), wsHost)
	return fmt.Sprintf(", network: ws, udp: true, ws-opts: %s", wsOpt)
}

// BuildClashConfig 生成包含多个节点、可手动切换的 clash 配置。
func BuildClashConfig(nodes []Node, password, localWS string) string {
	var proxies []string
	var names []string
	seen := map[string]int{}
	for _, n := range nodes {
		if n.Server == "" || n.Port == 0 {
			continue
		}
		sni := n.Sni
		if sni == "" {
			sni = n.Server
		}
		name := n.Name
		if name == "" {
			name = fmt.Sprintf("%s:%d", n.Server, n.Port)
		}
		seen[name]++
		if seen[name] > 1 {
			name = fmt.Sprintf("%s-%d", name, seen[name])
		}
		ws := ""
		if n.Local {
			ws = localWS
		}
		proxies = append(proxies, fmt.Sprintf("  - {name: %s, server: %s, port: %d, type: trojan, password: %s, sni: %s%s}",
			yamlQuote(name), yamlQuote(n.Server), n.Port, yamlQuote(password), yamlQuote(sni), ws))
		names = append(names, "      - "+yamlQuote(name))
	}
	if len(proxies) == 0 {
		return ""
	}
	return fmt.Sprintf("proxies:\n%s\n\nproxy-groups:\n  - name: PROXY\n    type: select\n    proxies:\n%s\n",
		strings.Join(proxies, "\n"), strings.Join(names, "\n"))
}

// NodesForUser 返回该用户可分享的节点。未分配过时只返回本机。
func NodesForUser(username string) []Node {
	nodes := LoadNodes()
	ids, ok, err := userNodeIDs(username)
	if err != nil || !ok {
		return localOnly(nodes)
	}
	selected := make([]Node, 0, len(ids))
	byID := map[string]Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	for _, id := range ids {
		if n, exists := byID[id]; exists {
			selected = append(selected, n)
		}
	}
	if len(selected) == 0 {
		return localOnly(nodes)
	}
	return selected
}

func localOnly(nodes []Node) []Node {
	for _, n := range nodes {
		if n.Local || n.ID == localNodeID {
			return []Node{n}
		}
	}
	if len(nodes) > 0 {
		return []Node{nodes[0]}
	}
	return []Node{currentLocalNode()}
}
