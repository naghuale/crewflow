//go:build darwin && cgo

package secret

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>
#include <string.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

// The calls of the framework, with the marshalling of CoreFoundation done here and not
// in Go: a dictionary of keys and values is a C thing, and the memory of Go is not a
// value of one. Every string and every value of a secret goes into the framework as a
// copy made in C, and a value of a secret comes back as a buffer of C that Go takes
// into its own memory and frees here.

static CFStringRef crewflowString(const char *text) {
	return CFStringCreateWithCString(kCFAllocatorDefault, text, kCFStringEncodingUTF8);
}

static CFDictionaryRef crewflowMatch(const char *service, const char *account, int withData) {
	CFStringRef name = crewflowString(service);
	CFStringRef who = crewflowString(account);
	// The key and the value callbacks are what make the dictionary hold on to what is
	// put into it: a mutable dictionary made with NULL callbacks keeps no reference at
	// all, and the two strings below would be released with the pointers to them still
	// inside the match that goes to the framework.
	CFMutableDictionaryRef match = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(match, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(match, kSecAttrService, name);
	CFDictionarySetValue(match, kSecAttrAccount, who);
	if (withData) {
		CFDictionarySetValue(match, kSecMatchLimit, kSecMatchLimitOne);
		CFDictionarySetValue(match, kSecReturnData, kCFBooleanTrue);
	}
	// The dictionary holds its own reference now, and this one is not its own.
	CFRelease(name);
	CFRelease(who);
	return match;
}

// crewflowGet answers with the value of a secret in a buffer of C and its length. The
// caller frees the buffer with crewflowFree.
static int crewflowGet(const char *service, const char *account, unsigned char **out, long *outLength) {
	CFDictionaryRef query = crewflowMatch(service, account, 1);
	CFTypeRef found = NULL;
	OSStatus status = SecItemCopyMatching(query, &found);
	CFRelease(query);
	if (status != errSecSuccess) {
		return (int)status;
	}
	if (CFGetTypeID(found) != CFDataGetTypeID()) {
		// The framework answered with something that is not data. Reading it as data
		// would be a guess, and the guess is released here on the way out: nothing of
		// the answer of the framework is left behind, whatever it was.
		CFRelease(found);
		return (int)errSecInternalError;
	}
	CFDataRef data = (CFDataRef)found;
	long length = (long)CFDataGetLength(data);
	unsigned char *copy = (unsigned char *)malloc((size_t)length + 1);
	if (copy == NULL) {
		CFRelease(found);
		return (int)errSecAllocate;
	}
	memcpy(copy, CFDataGetBytePtr(data), (size_t)length);
	copy[length] = 0;
	CFRelease(found);
	*out = copy;
	*outLength = length;
	return (int)errSecSuccess;
}

// crewflowSet keeps a value, or changes the one that is kept: an item of a name that is
// not there yet is added, and one that is there is changed in place, so that a second
// import of a key of an app leaves the one key under that name.
static int crewflowSet(const char *service, const char *account, const unsigned char *value, long length) {
	CFStringRef name = crewflowString(service);
	CFStringRef who = crewflowString(account);
	CFDataRef data = CFDataCreate(kCFAllocatorDefault, (const UInt8 *)value, (CFIndex)length);
	CFDictionaryRef match = crewflowMatch(service, account, 0);
	// The callbacks are what keep the value alive for as long as the attributes are
	// there — the same reason as in crewflowMatch, and the value below is released
	// only at the end of this function.
	CFMutableDictionaryRef only = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(only, kSecValueData, data);
	OSStatus status = SecItemUpdate(match, only);
	if (status == errSecItemNotFound) {
		// There is no item of this name yet, and a whole item is made of the class,
		// the name and the value: the framework refuses a match that carries a search
		// limit, which is why this dictionary is not the match of the call above. The
		// callbacks keep every value of it alive while SecItemAdd is reading it.
		CFMutableDictionaryRef add = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
		CFDictionarySetValue(add, kSecClass, kSecClassGenericPassword);
		CFDictionarySetValue(add, kSecAttrService, name);
		CFDictionarySetValue(add, kSecAttrAccount, who);
		CFDictionarySetValue(add, kSecValueData, data);
		status = SecItemAdd(add, NULL);
		CFRelease(add);
		// An item that appeared between the two calls is the one that was asked for.
		if (status == errSecDuplicateItem) {
			status = errSecSuccess;
		}
	}
	CFRelease(only);
	CFRelease(match);
	CFRelease(data);
	CFRelease(name);
	CFRelease(who);
	return (int)status;
}

// crewflowHas asks for the status of an item alone, and the framework reads nothing for
// it: a report that says the key of an app is there does not read the key to say it.
static int crewflowHas(const char *service, const char *account) {
	CFDictionaryRef match = crewflowMatch(service, account, 0);
	OSStatus status = SecItemCopyMatching(match, NULL);
	CFRelease(match);
	return (int)status;
}

// crewflowError is what macOS says about a status, in the words of macOS. The caller
// frees the string with crewflowFree.
static char *crewflowError(int status) {
	CFStringRef reason = SecCopyErrorMessageString((OSStatus)status, NULL);
	if (reason == NULL) {
		return NULL;
	}
	char buffer[512];
	Boolean said = CFStringGetCString(reason, buffer, sizeof(buffer), kCFStringEncodingUTF8);
	CFRelease(reason);
	if (!said) {
		return NULL;
	}
	char *copy = (char *)malloc(strlen(buffer) + 1);
	if (copy == NULL) {
		return NULL;
	}
	strcpy(copy, buffer);
	return copy;
}

// crewflowDelete takes one item of the keychain away: what the store of a secret has no
// reason to do, and the one test of this package has every reason to.
static int crewflowDelete(const char *service, const char *account) {
	CFStringRef name = crewflowString(service);
	CFStringRef who = crewflowString(account);
	CFMutableDictionaryRef match = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(match, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(match, kSecAttrService, name);
	CFDictionarySetValue(match, kSecAttrAccount, who);
	OSStatus status = SecItemDelete(match);
	CFRelease(match);
	CFRelease(name);
	CFRelease(who);
	return (int)status;
}

static void crewflowFree(void *memory) { free(memory); }
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// The two codes of the framework crewflow tells apart: it did what was asked, and there
// is no such item. The rest is said in the words of macOS, which crewflow does not
// guess (docs/DESIGN.md §7i).
const (
	// statusYes is errSecSuccess of Security: the call did what it was asked.
	statusYes = 0
	// statusNoItem is errSecItemNotFound of Security: there is no item of that name,
	// which is an empty store and not a store that could not be read.
	statusNoItem = -25300
)

// System returns the store of this machine: the keychain of macOS through
// Security.framework, which is where the key of an App of a project is kept on the
// only system crewflow has a store for (docs/DESIGN.md §7i).
//
// It is the framework itself and not the program `security`, and that is a decision
// and not a detail: `security add-generic-password -w` asks for the value of a new
// item on a terminal of its own and reads one line of it, and gives back what is not
// printable as a hexadecimal dump. A key of an App is many lines and is not printable
// in the sense of that program. Here it is written and read as the bytes it is, and
// nothing of it is ever an argument of a program that `ps` shows.
func System() Store { return keychain{} }

// keychain is the store of macOS: one generic password per secret, all of them under
// the service crewflow, each under the account that says what it is of.
type keychain struct{}

// Get returns what is kept under the service and the account, and [ErrNotFound] when
// there is nothing there. The value comes out of the framework as the bytes it was
// written as, and is read here and nowhere else: the key of an App is read in the
// moment a token is signed with it, and at no other time (docs/DESIGN.md §7i).
func (keychain) Get(service, account string) ([]byte, error) {
	name, who := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(name))
	defer C.free(unsafe.Pointer(who))
	var value *C.uchar
	var length C.long
	switch status := int(C.crewflowGet(name, who, &value, &length)); status {
	case statusYes:
		defer C.crewflowFree(unsafe.Pointer(value))
		return C.GoBytes(unsafe.Pointer(value), C.int(length)), nil
	case statusNoItem:
		return nil, fmt.Errorf("%s/%s: %w", service, account, ErrNotFound)
	default:
		return nil, keychainRefused("read", service, account, status)
	}
}

// Set keeps the value under the service and the account, replacing what was there: a
// second item under the same name would be a secret nothing signs with, and an owner
// who imports a new key of an App expects the new one to be the one a run signs with.
func (keychain) Set(service, account string, value []byte) error {
	if len(value) == 0 {
		return fmt.Errorf("%s/%s: nothing to keep: a secret of an empty value is not one", service, account)
	}
	name, who := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(name))
	defer C.free(unsafe.Pointer(who))
	if status := int(C.crewflowSet(name, who, (*C.uchar)(unsafe.Pointer(&value[0])), C.long(len(value)))); status != statusYes {
		return keychainRefused("keep", service, account, status)
	}
	return nil
}

// Has reports whether a value is kept under the service and the account, without
// reading it: the framework is asked for the status of the item alone, and a report
// that says the key of an App is there says all a person needs before a run without
// reading a key to say it (docs/DESIGN.md §7e, §7i).
func (keychain) Has(service, account string) (bool, error) {
	name, who := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(name))
	defer C.free(unsafe.Pointer(who))
	switch status := int(C.crewflowHas(name, who)); status {
	case statusYes:
		return true, nil
	case statusNoItem:
		return false, nil
	default:
		return false, keychainRefused("look for", service, account, status)
	}
}

// delete takes an item of the keychain away. It is not a part of [Store] and crewflow
// has no reason to take a secret away: what it does with a key of an App is to keep it.
// It is here for the one test of this package that writes into the keychain of the
// person who runs it, because an item of a test has to be gone when the test is over.
func (keychain) delete(service, account string) error {
	name, who := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(name))
	defer C.free(unsafe.Pointer(who))
	switch status := int(C.crewflowDelete(name, who)); status {
	case statusYes, statusNoItem:
		return nil
	default:
		return keychainRefused("take", service, account, status)
	}
}

// The keychain of macOS is the store of this machine, and it can say that a value is
// there without reading it, which is what a report about a key of an App asks for.
var (
	_ Store    = keychain{}
	_ Presence = keychain{}
)

// keychainRefused is what macOS said, in the words of macOS: a code and a sentence are
// two very different things to act on, and the sentence is the one a person can act
// on. The key of an App is never in it — macOS was never given any.
func keychainRefused(what, service, account string, status int) error {
	detail := ""
	if reason := C.crewflowError(C.int(status)); reason != nil {
		defer C.crewflowFree(unsafe.Pointer(reason))
		detail = ": " + C.GoString(reason)
	}
	return fmt.Errorf("the keychain of macOS refused to %s %s/%s (status %d)%s",
		what, service, account, status, detail)
}
