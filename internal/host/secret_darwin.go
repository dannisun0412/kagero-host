//go:build darwin

package host

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>
static CFMutableDictionaryRef query(const char *account) {
 CFMutableDictionaryRef q=CFDictionaryCreateMutable(NULL,0,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 CFStringRef a=CFStringCreateWithCString(NULL,account,kCFStringEncodingUTF8);
 CFDictionarySetValue(q,kSecClass,kSecClassGenericPassword);
 CFDictionarySetValue(q,kSecAttrService,CFSTR("app.kagero.host.identity"));
 CFDictionarySetValue(q,kSecAttrAccount,a); CFRelease(a); return q;
}
static int read_key(const char *account, void **out, long *len) {
 CFMutableDictionaryRef q=query(account); CFDictionarySetValue(q,kSecReturnData,kCFBooleanTrue);
 CFDataRef data=NULL; OSStatus st=SecItemCopyMatching(q,(CFTypeRef*)&data); CFRelease(q);
 if(st==0 && data) { *len=CFDataGetLength(data); *out=malloc(*len); if(!*out){CFRelease(data);return -1;} memcpy(*out,CFDataGetBytePtr(data),*len); CFRelease(data); } return st;
}
static int write_key(const char *account, const void *bytes, long len) {
 CFMutableDictionaryRef q=query(account); CFDataRef data=CFDataCreate(NULL,bytes,len);
 CFMutableDictionaryRef update=CFDictionaryCreateMutable(NULL,0,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 CFDictionarySetValue(update,kSecValueData,data); OSStatus st=SecItemUpdate(q,update);
 if(st==errSecItemNotFound){ CFDictionarySetValue(q,kSecValueData,data); st=SecItemAdd(q,NULL); }
 CFRelease(update);CFRelease(data);CFRelease(q); return st;
}
*/
import "C"
import (
	"crypto/sha256"
	"errors"
	"fmt"
	"unsafe"
)

var errSecretMissing = errors.New("主机钥匙串记录不存在")

func secretAccount(dir string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(dir))) }
func readSecret(dir string) ([]byte, error) {
	a := C.CString(secretAccount(dir))
	defer C.free(unsafe.Pointer(a))
	var out unsafe.Pointer
	var size C.long
	status := C.read_key(a, &out, &size)
	if status == C.errSecItemNotFound {
		return nil, errSecretMissing
	}
	if status != 0 {
		return nil, fmt.Errorf("无法读取主机钥匙串（%d），请解锁 macOS 钥匙串", status)
	}
	defer C.free(out)
	if size <= 0 || size > 8192 {
		return nil, errors.New("主机钥匙串记录大小无效")
	}
	return C.GoBytes(out, C.int(size)), nil
}
func writeSecret(dir string, data []byte) error {
	a := C.CString(secretAccount(dir))
	defer C.free(unsafe.Pointer(a))
	bytes := C.CBytes(data)
	defer C.free(bytes)
	status := C.write_key(a, bytes, C.long(len(data)))
	if status != 0 {
		return fmt.Errorf("无法保存主机钥匙串（%d）", status)
	}
	return nil
}
