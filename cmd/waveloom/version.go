package main

// Version 是 Waveloom 的版本号。
// 编译时通过 ldflags 注入：-ldflags "-X main.Version=0.1.0"
// 未注入时默认 "dev"。
var Version = "dev"
