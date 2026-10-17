package outbound

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var winHTTP = windows.NewLazySystemDLL("winhttp.dll")
var getIEProxy = winHTTP.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
var getURLProxy = winHTTP.NewProc("WinHttpGetProxyForUrl")
var openWinHTTP = winHTTP.NewProc("WinHttpOpen")
var closeWinHTTP = winHTTP.NewProc("WinHttpCloseHandle")
var timeoutWinHTTP = winHTTP.NewProc("WinHttpSetTimeouts")
var freeProxyMemory = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")

type windowsProxyConfig struct {
	AutoDetect         uint32
	PAC, Proxy, Bypass *uint16
}
type windowsProxyInfo struct {
	AccessType    uint32
	Proxy, Bypass *uint16
}
type windowsAutoOptions struct {
	Flags, AutoDetectFlags   uint32
	URL                      *uint16
	Reserved                 uintptr
	ReservedFlags, AutoLogon uint32
}

func systemProxy(req *http.Request) (*url.URL, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	var cfg windowsProxyConfig
	ok, _, err := getIEProxy.Call(uintptr(unsafe.Pointer(&cfg)))
	if ok == 0 {
		return nil, fmt.Errorf("read system proxy: %w", err)
	}
	defer freeWindowsProxy(cfg.PAC, cfg.Proxy, cfg.Bypass)
	raw, bypass := windows.UTF16PtrToString(cfg.Proxy), windows.UTF16PtrToString(cfg.Bypass)
	if cfg.PAC != nil || cfg.AutoDetect != 0 {
		handle, _, err := openWinHTTP.Call(0, 1, 0, 0, 0)
		if handle == 0 {
			return nil, fmt.Errorf("open system proxy resolver: %w", err)
		}
		defer closeWinHTTP.Call(handle)
		timeoutWinHTTP.Call(handle, 3000, 3000, 3000, 3000)
		options := windowsAutoOptions{Flags: 2, AutoDetectFlags: 3}
		if cfg.PAC != nil {
			options.Flags = 1
			options.URL = cfg.PAC
		}
		target, err := windows.UTF16PtrFromString(req.URL.String())
		if err != nil {
			return nil, err
		}
		var info windowsProxyInfo
		ok, _, err := getURLProxy.Call(handle, uintptr(unsafe.Pointer(target)), uintptr(unsafe.Pointer(&options)), uintptr(unsafe.Pointer(&info)))
		defer freeWindowsProxy(info.Proxy, info.Bypass)
		if ok == 0 {
			if cfg.PAC == nil && err == syscall.Errno(12180) {
				return staticSystemProxy(req, windowsProxyForScheme(raw, req.URL.Scheme), strings.Split(bypass, ";"))
			}
			return nil, fmt.Errorf("resolve system proxy: %w", err)
		}
		raw, bypass = windows.UTF16PtrToString(info.Proxy), windows.UTF16PtrToString(info.Bypass)
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return staticSystemProxy(req, windowsProxyForScheme(raw, req.URL.Scheme), strings.Split(bypass, ";"))
}

func freeWindowsProxy(values ...*uint16) {
	for _, value := range values {
		if value != nil {
			freeProxyMemory.Call(uintptr(unsafe.Pointer(value)))
		}
	}
}
