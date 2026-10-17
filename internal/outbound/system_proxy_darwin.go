//go:build darwin && cgo

package outbound

/*
#cgo LDFLAGS: -framework CoreFoundation -framework CFNetwork
#include <CoreFoundation/CoreFoundation.h>
#include <CFNetwork/CFNetwork.h>
#include <stdlib.h>
#include <string.h>

static void clipalPAC(void *info, CFArrayRef proxies, CFErrorRef error) {
 CFTypeRef *result = info;
 if (error) *result = CFRetain(error);
 else if (proxies) *result = CFRetain(proxies);
 CFRunLoopStop(CFRunLoopGetCurrent());
}
// Returns a retained first proxy dictionary, including kCFProxyTypeNone.
// CFNetwork applies the current system exceptions and per-URL proxy policy.
static CFDictionaryRef clipalProxy(const char *target, const char *settingsXML, double timeout) {
 CFStringRef text = CFStringCreateWithCString(NULL, target, kCFStringEncodingUTF8);
 if (!text) return NULL;
 CFURLRef url = CFURLCreateWithString(NULL, text, NULL);
 CFRelease(text);
 if (!url) return NULL;
 CFDictionaryRef settings = NULL;
 if (settingsXML) {
  CFDataRef data = CFDataCreate(NULL, (const UInt8 *)settingsXML, strlen(settingsXML));
  CFPropertyListRef plist = CFPropertyListCreateWithData(NULL, data, kCFPropertyListImmutable, NULL, NULL);
  CFRelease(data);
  if (plist && CFGetTypeID(plist) == CFDictionaryGetTypeID()) settings = (CFDictionaryRef)plist;
  else if (plist) CFRelease(plist);
 } else settings = CFNetworkCopySystemProxySettings();
 if (!settings) { CFRelease(url); return NULL; }
 CFArrayRef proxies = CFNetworkCopyProxiesForURL(url, settings);
 CFRelease(settings);
 CFDictionaryRef result = NULL;
 if (proxies && CFArrayGetCount(proxies)) {
  CFDictionaryRef first = CFArrayGetValueAtIndex(proxies, 0);
  CFStringRef type = CFDictionaryGetValue(first, kCFProxyTypeKey);
  if (type && (CFEqual(type, kCFProxyTypeAutoConfigurationURL) || CFEqual(type, kCFProxyTypeAutoConfigurationJavaScript))) {
   CFTypeRef evaluated = NULL;
   CFStreamClientContext context = {0, &evaluated, NULL, NULL, NULL};
   CFRunLoopSourceRef source = NULL;
   if (CFEqual(type, kCFProxyTypeAutoConfigurationURL)) {
    CFURLRef pac = CFDictionaryGetValue(first, kCFProxyAutoConfigurationURLKey);
    if (pac) source = CFNetworkExecuteProxyAutoConfigurationURL(pac, url, clipalPAC, &context);
   } else {
    CFStringRef script = CFDictionaryGetValue(first, kCFProxyAutoConfigurationJavaScriptKey);
    if (script) source = CFNetworkExecuteProxyAutoConfigurationScript(script, url, clipalPAC, &context);
   }
   if (source) {
    CFStringRef mode = CFSTR("clipal-proxy");
    CFRunLoopRef loop = CFRunLoopGetCurrent();
    CFRunLoopAddSource(loop, source, mode);
    CFRunLoopRunInMode(mode, timeout, false);
    CFRunLoopSourceInvalidate(source);
    CFRunLoopRemoveSource(loop, source, mode);
    CFRelease(source);
   }
   if (evaluated) {
    if (CFGetTypeID(evaluated) == CFArrayGetTypeID() && CFArrayGetCount((CFArrayRef)evaluated))
     result = (CFDictionaryRef)CFRetain(CFArrayGetValueAtIndex((CFArrayRef)evaluated, 0));
    CFRelease(evaluated);
   }
  } else result = (CFDictionaryRef)CFRetain(first);
 }
 if (proxies) CFRelease(proxies);
 CFRelease(url);
 return result;
}
static char *clipalString(CFDictionaryRef dict, CFStringRef key) {
 CFStringRef str = CFDictionaryGetValue(dict, key);
 if (!str || CFGetTypeID(str) != CFStringGetTypeID()) return NULL;
 CFIndex size = CFStringGetMaximumSizeForEncoding(CFStringGetLength(str), kCFStringEncodingUTF8) + 1;
 char *out = malloc(size);
 if (!CFStringGetCString(str, out, size, kCFStringEncodingUTF8)) { free(out); return NULL; }
 return out;
}
static int clipalPort(CFDictionaryRef dict) {
 CFNumberRef number = CFDictionaryGetValue(dict, kCFProxyPortNumberKey);
 int port = 0;
 if (number) CFNumberGetValue(number, kCFNumberIntType, &port);
 return port;
}
static int clipalType(CFDictionaryRef dict) {
 CFStringRef type = CFDictionaryGetValue(dict, kCFProxyTypeKey);
 if (!type) return -1;
 if (CFEqual(type, kCFProxyTypeNone)) return 0;
 if (CFEqual(type, kCFProxyTypeHTTP)) return 1;
 if (CFEqual(type, kCFProxyTypeHTTPS)) return 2;
 if (CFEqual(type, kCFProxyTypeSOCKS)) return 3;
 return -1;
}
*/
import "C"

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"time"
	"unsafe"
)

func systemProxy(req *http.Request) (*url.URL, error) {
	return darwinSystemProxy(req, "")
}

// A plist snapshot lets native selection be verified without changing OS settings.
func darwinSystemProxy(req *http.Request, settings string) (*url.URL, error) {
	timeout := 3 * time.Second
	if deadline, ok := req.Context().Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	raw := C.CString(req.URL.String())
	defer C.free(unsafe.Pointer(raw))
	// PAC callbacks and the run loop must remain on the same native thread.
	runtime.LockOSThread()
	var snapshot *C.char
	if settings != "" {
		snapshot = C.CString(settings)
		defer C.free(unsafe.Pointer(snapshot))
	}
	dict := C.clipalProxy(raw, snapshot, C.double(timeout.Seconds()))
	runtime.UnlockOSThread()
	if dict == 0 {
		return nil, fmt.Errorf("system proxy resolution failed or timed out")
	}
	defer C.CFRelease(C.CFTypeRef(dict))
	kind := int(C.clipalType(dict))
	if kind == 0 {
		return nil, nil
	}
	// CFNetwork's HTTPS type denotes HTTP CONNECT for an HTTPS target,
	// not a TLS connection to the proxy itself (CFProxySupport.h).
	schemes := map[int]string{1: "http", 2: "http", 3: "socks5h"}
	scheme, ok := schemes[kind]
	if !ok {
		return nil, fmt.Errorf("unsupported system proxy type")
	}
	get := func(key C.CFStringRef) string {
		value := C.clipalString(dict, key)
		if value == nil {
			return ""
		}
		defer C.free(unsafe.Pointer(value))
		return C.GoString(value)
	}
	host := get(C.kCFProxyHostNameKey)
	port := int(C.clipalPort(dict))
	if host == "" || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid system proxy endpoint")
	}
	result := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(port))}
	if user := get(C.kCFProxyUsernameKey); user != "" {
		result.User = url.UserPassword(user, get(C.kCFProxyPasswordKey))
	}
	return result, nil
}
