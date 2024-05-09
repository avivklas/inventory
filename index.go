package inventory

import (
	"bytes"
	"strings"
	"sync"
)

const (
	curlyStart = '{'
	curlyEnd   = '}'
	colon      = ':'
)

var bufPool = sync.Pool{New: func() any {
	return &bytes.Buffer{}
}}

func mkKey(kind, key, val string) (res string) {
	buf := bufPool.Get().(*bytes.Buffer)
	writeKey(buf, kind, key, val)
	res = buf.String()
	buf.Reset()
	bufPool.Put(buf)

	return
}

func parseKey(key string) (kind, k, v string, ok bool) {
	endOfKind := strings.IndexByte(key, curlyStart)
	if endOfKind < 0 {
		return
	}

	kind = key[:endOfKind]

	endOfKey := strings.IndexByte(key, colon)
	if endOfKey < 0 {
		return
	}

	k = key[endOfKind+1 : endOfKey]

	endOfKeyVal := strings.IndexByte(key, curlyEnd)
	if endOfKeyVal < 0 {
		return
	}

	v = key[endOfKey+1 : endOfKeyVal]
	ok = true

	return
}

func writeKey(dst *bytes.Buffer, kind, key, val string) {
	dst.WriteString(kind)
	dst.WriteRune(curlyStart)
	dst.WriteString(key)
	dst.WriteRune(colon)
	dst.WriteString(val)
	dst.WriteRune(curlyEnd)
}
