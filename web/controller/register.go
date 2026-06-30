package controller

import (
	"time"
	"trojan/core"
)

// GetRegisterStatus 公开接口: 仅返回自助注册是否开放, 不泄露邀请码
func GetRegisterStatus() *ResponseBody {
	responseBody := ResponseBody{Msg: "success"}
	defer TimeCost(time.Now(), &responseBody)
	enabled, _ := core.GetValue("register_enabled")
	code, _ := core.GetValue("register_code")
	responseBody.Data = map[string]interface{}{
		"enabled": enabled == "true" && code != "",
	}
	return &responseBody
}

// GetRegisterConfig 管理员接口: 获取自助注册配置
func GetRegisterConfig() *ResponseBody {
	responseBody := ResponseBody{Msg: "success"}
	defer TimeCost(time.Now(), &responseBody)
	enabled, _ := core.GetValue("register_enabled")
	code, _ := core.GetValue("register_code")
	responseBody.Data = map[string]interface{}{
		"enabled": enabled == "true",
		"code":    code,
	}
	return &responseBody
}

// SetRegisterConfig 管理员接口: 设置自助注册开关和邀请码
func SetRegisterConfig(enabled bool, code string) *ResponseBody {
	responseBody := ResponseBody{Msg: "success"}
	defer TimeCost(time.Now(), &responseBody)
	enabledStr := "false"
	if enabled {
		enabledStr = "true"
	}
	if err := core.SetValue("register_enabled", enabledStr); err != nil {
		responseBody.Msg = err.Error()
		return &responseBody
	}
	if err := core.SetValue("register_code", code); err != nil {
		responseBody.Msg = err.Error()
	}
	return &responseBody
}

// SignUp 访客自助注册trojan用户(公开接口, 需通过开关与邀请码校验)
func SignUp(username, password, code string) *ResponseBody {
	responseBody := ResponseBody{Msg: "success"}
	enabled, _ := core.GetValue("register_enabled")
	storeCode, _ := core.GetValue("register_code")
	if enabled != "true" || storeCode == "" {
		responseBody.Msg = "自助注册未开放"
		return &responseBody
	}
	if code != storeCode {
		responseBody.Msg = "邀请码错误"
		return &responseBody
	}
	// 通过门禁后复用管理员创建用户的逻辑(含用户名/密码合法性与重复校验)
	return CreateUser(username, password)
}
