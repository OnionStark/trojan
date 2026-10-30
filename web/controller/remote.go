package controller

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// remoteClient 使用对方面板现有的登录和用户接口同步账号。
// 面板证书经常是自签证书，或域名和证书不一致，因此校验由管理员填写的地址本身保证。
type remoteClient struct {
	http *http.Client
}

func newRemoteClient() *remoteClient {
	return &remoteClient{
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

var defaultSyncer nodeSyncer = newRemoteClient()

type remoteUser struct {
	ID       uint   `json:"ID"`
	Username string `json:"Username"`
}

type remoteMessage struct {
	Msg     string `json:"Msg"`
	Message string `json:"message"`
	Code    int    `json:"code"`
	Token   string `json:"token"`
	Data    struct {
		UserList []remoteUser `json:"userList"`
	} `json:"Data"`
}

func (c *remoteClient) do(req *http.Request) (remoteMessage, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return remoteMessage{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return remoteMessage{}, err
	}
	var msg remoteMessage
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &msg); err != nil {
			return remoteMessage{}, fmt.Errorf("节点返回无法解析: %s", strings.TrimSpace(string(body)))
		}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		text := msg.Message
		if text == "" {
			text = "管理员账号或密码错误"
		}
		return msg, errors.New(text)
	}
	if resp.StatusCode >= 400 {
		text := msg.Msg
		if text == "" {
			text = msg.Message
		}
		if text == "" {
			text = resp.Status
		}
		return msg, errors.New(text)
	}
	if msg.Msg != "" && msg.Msg != "success" {
		return msg, errors.New(msg.Msg)
	}
	return msg, nil
}

func (c *remoteClient) login(node Node) (string, error) {
	payload, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": node.AdminPass,
	})
	req, err := http.NewRequest(http.MethodPost, node.PanelURL+"/auth/login", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	msg, err := c.do(req)
	if err != nil {
		return "", fmt.Errorf("登录失败: %s", err.Error())
	}
	if msg.Token == "" {
		return "", errors.New("登录失败: 未返回凭证")
	}
	return msg.Token, nil
}

func (c *remoteClient) listUsers(node Node, token string) ([]remoteUser, error) {
	req, err := http.NewRequest(http.MethodGet, node.PanelURL+"/trojan/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	msg, err := c.do(req)
	if err != nil {
		return nil, err
	}
	return msg.Data.UserList, nil
}

func findRemoteUser(users []remoteUser, names ...string) (remoteUser, bool) {
	for _, name := range names {
		if name == "" {
			continue
		}
		for _, user := range users {
			if user.Username == name {
				return user, true
			}
		}
	}
	return remoteUser{}, false
}

func (c *remoteClient) writeUser(node Node, token, method, path string, form url.Values) error {
	req, err := http.NewRequest(method, node.PanelURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	_, err = c.do(req)
	return err
}

func (c *remoteClient) createUser(node Node, token, username, passB64 string) error {
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", passB64)
	return c.writeUser(node, token, http.MethodPost, "/trojan/user", form)
}

func (c *remoteClient) updateUser(node Node, token string, id uint, username, passB64 string) error {
	form := url.Values{}
	form.Set("id", strconv.FormatUint(uint64(id), 10))
	form.Set("username", username)
	form.Set("password", passB64)
	return c.writeUser(node, token, http.MethodPost, "/trojan/user/update", form)
}

func (c *remoteClient) restart(node Node, token string) error {
	req, err := http.NewRequest(http.MethodPost, node.PanelURL+"/trojan/restart", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	_, err = c.do(req)
	return err
}

// Ensure 在远程面板创建账号；已存在则改为相同的用户名和密码，并重启对方的 trojan。
func (c *remoteClient) Ensure(node Node, oldName, newName, passB64 string) error {
	token, err := c.login(node)
	if err != nil {
		return err
	}
	users, err := c.listUsers(node, token)
	if err != nil {
		return err
	}
	if user, ok := findRemoteUser(users, oldName, newName); ok {
		if err := c.updateUser(node, token, user.ID, newName, passB64); err != nil {
			return err
		}
		if err := c.restart(node, token); err != nil {
			return fmt.Errorf("账号已更新，重启失败: %s", err.Error())
		}
		return nil
	}
	if err := c.createUser(node, token, newName, passB64); err != nil {
		if !strings.Contains(err.Error(), "已存在用户名") {
			return err
		}
		users, listErr := c.listUsers(node, token)
		if listErr != nil {
			return listErr
		}
		user, ok := findRemoteUser(users, oldName, newName)
		if !ok {
			return err
		}
		if err := c.updateUser(node, token, user.ID, newName, passB64); err != nil {
			return err
		}
		if err := c.restart(node, token); err != nil {
			return fmt.Errorf("账号已更新，重启失败: %s", err.Error())
		}
		return nil
	}
	return nil
}

// Remove 删除远程面板上的账号。对方接口会在删除后重启 trojan。账号本来就不存在时视为成功。
func (c *remoteClient) Remove(node Node, username string) error {
	token, err := c.login(node)
	if err != nil {
		return err
	}
	users, err := c.listUsers(node, token)
	if err != nil {
		return err
	}
	user, ok := findRemoteUser(users, username)
	if !ok {
		return nil
	}
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/trojan/user?id=%d", node.PanelURL, user.ID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	_, err = c.do(req)
	return err
}
