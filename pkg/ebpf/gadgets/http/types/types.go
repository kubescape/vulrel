package types

import (
	"fmt"
	"net"
	"net/http"

	"github.com/inspektor-gadget/inspektor-gadget/pkg/columns"
	eventtypes "github.com/inspektor-gadget/inspektor-gadget/pkg/types"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/consts"
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

type HTTPRequestData struct {
	Method  string
	URL     string
	Headers http.Header
}
type HTTPResponseData struct {
	StatusCode int
	Status     string
	Headers    http.Header
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
}

func GetPacketDirection(event *Event) (consts.NetworkDirection, error) {
	if readSyscalls[event.Syscall] {
		return consts.Inbound, nil
	} else if writeSyscalls[event.Syscall] {
		return consts.Outbound, nil
	} else {
		return "", fmt.Errorf("unknown syscall %s", event.Syscall)
	}
}

func IsInternal(ip string) bool {
	ipAddress := net.ParseIP(ip)
	return ipAddress.IsPrivate()
}

func GetColumns() *columns.Columns[Event] {
	httpColumns := columns.MustCreateColumns[Event]()

	return httpColumns
}

func Base(ev eventtypes.Event) *Event {
	return &Event{
		Event: ev,
	}
}