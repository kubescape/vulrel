package config

import (
	"os"
	"testing"
	"time"
)

func TestIsFalcoEbpfEngine(t *testing.T) {
	c := CreateConfigData()
	if c.IsFalcoEbpfEngine() {
		t.Errorf("Expected false, got true")
	}

	c.FalcoEbpfEngineData.EbpfEngineLoaderPath = "/path/to/loader"
	c.FalcoEbpfEngineData.KernelObjPath = "/path/to/loader"
	if !c.IsFalcoEbpfEngine() {
		t.Errorf("Expected true, got false")
	}
}

func TestSetFalcoSyscallFilter(t *testing.T) {
	c := CreateConfigData()
	c.FeatureList = []SnifferServices{
		{Name: "relevantCVEs"},
		{Name: "otherService"},
	}
	c.setFalcoSyscallFilter()
	if len(falcoSyscallFilter) != 0 {
		t.Errorf("Expected empty list")
	}

	c.FalcoEbpfEngineData.EbpfEngineLoaderPath = "/path/to/loader"
	c.FalcoEbpfEngineData.KernelObjPath = "/path/to/loader"
	c.setFalcoSyscallFilter()
	expected := []string{"open", "openat", "execve", "execveat"}
	if !equalStringSlices(falcoSyscallFilter, expected) {
		t.Errorf("Expected %v, got %v", expected, falcoSyscallFilter)
	}

}

func TestGetFalcoSyscallFilter(t *testing.T) {
	c := CreateConfigData()
	c.FeatureList = []SnifferServices{
		{Name: "relevantCVEs"},
		{Name: "otherService"},
	}

	filter := c.GetFalcoSyscallFilter()
	expected := []string{"open", "openat", "execve", "execveat"}
	if !equalStringSlices(filter, expected) {
		t.Errorf("Expected %v, got %v", expected, filter)
	}

	// Ensure that the filter is cached
	falcoSyscallFilter = []string{"other", "syscall"}
	filter = c.GetFalcoSyscallFilter()
	if !equalStringSlices(filter, []string{"other", "syscall"}) {
		t.Errorf("Expected [other syscall], got %v", filter)
	}
}

func TestGetFalcoKernelObjPath(t *testing.T) {
	c := CreateConfigData()
	c.FalcoEbpfEngineData.KernelObjPath = "/path/to/kernel/obj"
	if path := c.GetFalcoKernelObjPath(); path != "/path/to/kernel/obj" {
		t.Errorf("Expected /path/to/kernel/obj, got %v", path)
	}
}

func TestGetEbpfEngineLoaderPath(t *testing.T) {
	c := CreateConfigData()
	c.FalcoEbpfEngineData.EbpfEngineLoaderPath = "/path/to/loader"
	if path := c.GetEbpfEngineLoaderPath(); path != "/path/to/loader" {
		t.Errorf("Expected /path/to/loader, got %v", path)
	}
}

func TestGetUpdateDataPeriod(t *testing.T) {
	c := CreateConfigData()
	c.DB.UpdateDataPeriod = 60
	if dur := c.GetUpdateDataPeriod(); dur != 60*time.Second {
		t.Errorf("Expected 60s, got %v", dur)
	}
}

func TestGetSniffingMaxTimes(t *testing.T) {
	c := CreateConfigData()
	c.SnifferData.SniffingMaxTime = 5
	if dur := c.GetSniffingMaxTimes(); dur != 5*time.Minute {
		t.Errorf("Expected 5m, got %v", dur)
	}
}

func TestIsRelevantCVEServiceEnabled(t *testing.T) {
	c := CreateConfigData()
	if c.IsRelevantCVEServiceEnabled() {
		t.Errorf("Expected true, got false")
	}

	c.FeatureList = []SnifferServices{
		{Name: "relevantCVEs"},
		{Name: "otherService"},
	}

	if !c.IsRelevantCVEServiceEnabled() {
		t.Errorf("Expected true, got false")
	}

}

func TestConfigData_GetNodeName(t *testing.T) {
	expectedName := "node-1"
	c := &ConfigData{
		NodeData: NodeData{Name: expectedName},
	}
	if c.GetNodeName() != expectedName {
		t.Errorf("GetNodeName() returned %s, expected %s", c.GetNodeName(), expectedName)
	}
}

func TestConfigData_GetClusterName(t *testing.T) {
	expectedName := "cluster-1"
	c := &ConfigData{
		ClusterName: expectedName,
	}
	if c.GetClusterName() != expectedName {
		t.Errorf("GetClusterName() returned %s, expected %s", c.GetClusterName(), expectedName)
	}
}

func TestConfigData_SetNodeName(t *testing.T) {
	expectedName := "node-1"
	os.Setenv(nodeNameEnvVar, expectedName)
	c := &ConfigData{}
	c.SetNodeName()
	if c.NodeData.Name != expectedName {
		t.Errorf("SetNodeName() failed to set the node name to %s, got %s instead", expectedName, c.NodeData.Name)
	}
}

// check is slices are equal
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
