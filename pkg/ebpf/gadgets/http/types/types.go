package types

import (
	"net/http"

	eventtypes "github.com/inspektor-gadget/inspektor-gadget/pkg/types"
)

const MaxGroupedEventSize int = 10000

type HTTPDataType int

const (
	Request  HTTPDataType = 2
	Response HTTPDataType = 3
)

var ConsistentHeaders = []string{
	"Accept-Encoding",
	"Accept-Language",
	"Connection",
	"Host",
	"Upgrade-Insecure-Requests",
}

var writeSyscalls = map[string]bool{
	"write":   true,
	"writev":  true,
	"sendto":  true,
	"sendmsg": true,
}

var readSyscalls = map[string]bool{
	"read":     true,
	"readv":    true,
	"recvfrom": true,
	"recvmsg":  true,
}

type HTTPData interface {
}

type HTTPRequest struct {
	Method  string
	URL     string
	Headers http.Header
}
type HTTPResponse struct {
	StatusCode int
	Status     string
	Headers    http.Header
}

type GroupedHTTP struct {
	Request  *HTTPRequest
	Response *HTTPResponse
}

type Event struct {
	eventtypes.Event
	eventtypes.WithMountNsID

	Pid       uint32   `json:"pid,omitempty" column:"pid,template:pid"`
	Uid       uint32   `json:"uid,omitempty" column:"uid,template:uid"`
	Gid       uint32   `json:"gid,omitempty" column:"gid,template:gid"`
	OtherPort uint16   `json:"other_port,omitempty" column:"other_port,template:other_port"`
	OtherIp   string   `json:"other_ip,omitempty" column:"other_ip,template:other_ip"`
	Syscall   string   `json:"syscall,omitempty" column:"syscall,template:syscall"`
	HttpData  HTTPData `json:"headers,omitempty" column:"headers,template:headers"`
	DataType  HTTPDataType
}
