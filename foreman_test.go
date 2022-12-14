package main

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"
)

const testProcfile = "tests_files/test_procfile.yaml"
const testProcfileMalform = "tests_files/test_malform_procfile.yaml"
const testHasCycleProcfile = "tests_files/test_hascycle_procfile.yaml"

var testProcfileStruct = map[string]Service {
	"app1": {
		info: ServiceInfo {
			cmd: "ping -c 1 google.com",
			runOnce: true,
			deps: []string {"redis6010"},
			checks: Check{cmd: "sleep 3"},
		},
	},
	"app2": {
		info: ServiceInfo {
			cmd: "ping -c 50 yahoo.com",
			runOnce: false,
			deps: []string {"redis8080"},
			checks: Check{cmd: "sleep 4"},
		},
	},
	"app3": {
		info: ServiceInfo {
			cmd: "sleep 10",
			runOnce: true,
			deps: []string {"app1"},
		},
	},
	"redis6010": {
		info: ServiceInfo {
			cmd: "redis-server --port 6010",
			runOnce: false,
			checks: Check{cmd: "redis-cli -p 6010 ping", tcpPorts: []string{"6010"}},
		},
	},
	"redis8080": {
		info: ServiceInfo {
			cmd: "redis-server --port 8080",
			runOnce: false,
			checks: Check{cmd: "redis-cli -p 8080 ping", tcpPorts: []string{"8080"}, udpPorts: []string{"80"}},
		},
	},
}

func TestInitForeman(t *testing.T) {
	t.Run("parse procfile successfully", func(t *testing.T) {
		foreman := Foreman {
			procfile: testProcfile,
			signalsChannel: make(chan os.Signal, MaxSizeChannel),
			servicesToRunChannel: make(chan string, MaxNumServices),
			checksTicker: time.NewTicker(TickInterval),
			services: map[string]Service{},
			servicesGraph: map[string][]string{},
		}

		foreman.parseProcfile()
		got := foreman.services
		want := testProcfileStruct

		assertEqualServices(t, got, want)
	})

	t.Run("fail to parse procfile", func(t *testing.T) {
		foremanMalformProcfile := Foreman {
			procfile: testProcfileMalform,
			signalsChannel: make(chan os.Signal, MaxSizeChannel),
			servicesToRunChannel: make(chan string, MaxNumServices),
			checksTicker: time.NewTicker(TickInterval),
			services: map[string]Service{},
			servicesGraph: map[string][]string{},
		}
		got := foremanMalformProcfile.parseProcfile()
		want := "yaml: unmarshal errors:\n  line 15: mapping key \"app1\" already defined at line 1"

		assertError(t, got, want)
	})

	t.Run("build services graph successfully", func(t *testing.T) {
		foreman := Foreman {
			procfile: testProcfile,
			signalsChannel: make(chan os.Signal, MaxSizeChannel),
			servicesToRunChannel: make(chan string, MaxNumServices),
			checksTicker: time.NewTicker(TickInterval),
			services: map[string]Service{},
			servicesGraph: map[string][]string{},
		}
		foreman.parseProcfile()
		foreman.buildServicesGraph()
		graphHasCycle(foreman.servicesGraph)
		got := foreman.servicesGraph
		want := map[string][]string {
			"app1": {"redis6010"},
			"app2": {"redis8080"},
			"app3": {},
			"redis6010": {},
			"redis8080": {},
		}
		assertEqualGraphs(t, got, want)
	})

	t.Run("services graph has cycle", func(t *testing.T) {
		foreman := Foreman {
			procfile: testHasCycleProcfile,
			signalsChannel: make(chan os.Signal, MaxSizeChannel),
			servicesToRunChannel: make(chan string, MaxNumServices),
			checksTicker: time.NewTicker(TickInterval),
			services: map[string]Service{},
			servicesGraph: map[string][]string{},
		}
		foreman.parseProcfile()
		foreman.buildServicesGraph()
		got, parentMap := graphHasCycle(foreman.servicesGraph)
		
		if got == false {
			t.Fatalf("expected to get %v but got %v\n", true, false)
		}

		cycleElements := getCycleElements(parentMap)
		sort.Strings(cycleElements)
		wantElements := []string{"app1", "redis6010"}

		if !reflect.DeepEqual(cycleElements, wantElements) {
			t.Fatalf("expected to get %v but got %v\n", wantElements, cycleElements)
		}
	})

	t.Run("toposort services", func(t *testing.T) {
		foreman := Foreman {
			procfile: testProcfile,
			signalsChannel: make(chan os.Signal, MaxSizeChannel),
			servicesToRunChannel: make(chan string, MaxNumServices),
			checksTicker: time.NewTicker(TickInterval),
			services: map[string]Service{},
			servicesGraph: map[string][]string{},
		}
		foreman.parseProcfile()
		foreman.buildServicesGraph()
		deps := foreman.topoSortServices()
		fmt.Println(deps)
		assertTopologicallySortedDeps(t, foreman, deps)
	})
}

func assertTopologicallySortedDeps(t *testing.T, foreman Foreman, got []string) {
	t.Helper()
	nodesSet := make(map[string]any)
	for _, dep := range got {
		for _, depDep := range foreman.services[dep].info.deps {
			if _, ok := nodesSet[depDep]; !ok {
				t.Fatalf("not expected to run %v before %v", dep, depDep)
			}
		}
		nodesSet[dep] = 1
	}
}

func assertEqualGraphs(t *testing.T, got, want map[string][]string) {
	t.Helper()

	for key, deps := range got {
		if !reflect.DeepEqual(deps, want[key]) {
			if len(deps) != 0 && len(want[key]) != 0 {
				t.Fatalf("got:\n%q\nwant:\n%q", deps, want[key])
			}
		}
	}
}

func assertEqualServices(t *testing.T, got, want map[string]Service) {
	t.Helper()
	for key, value := range got {
		if value.info.cmd != want[key].info.cmd {
			t.Fatalf("key: %v got cmd:%v\nwant cmd:%v", key, value.info.cmd, want[key].info.cmd)
		}
		if value.info.runOnce != want[key].info.runOnce {
			t.Fatalf("key: %v got runOnce:%v\nwant runOnce:%v", key, value.info.runOnce, want[key].info.runOnce)
		}
		if !reflect.DeepEqual(value.info.deps, want[key].info.deps) {
			t.Fatalf("key: %v got deps:%v\nwant deps:%v", key, value.info.deps, want[key].info.deps)
		}

		if value.info.checks.cmd != want[key].info.checks.cmd {
			t.Fatalf("key: %v got checkCmd:%v\nwant checkCmd:%v", key, value.info.checks.cmd, want[key].info.checks.cmd)
		}
		if !reflect.DeepEqual(value.info.checks.tcpPorts, want[key].info.checks.tcpPorts) {
			t.Fatalf("key: %v got tcp Ports:%v\nwant tcp Ports:%v", key, value.info.checks.tcpPorts, want[key].info.checks.tcpPorts)
		}
		if !reflect.DeepEqual(value.info.checks.udpPorts, want[key].info.checks.udpPorts) {
			t.Fatalf("key: %v got udp Ports:%v\nwant udp Ports:%v", key, value.info.checks.udpPorts, want[key].info.checks.udpPorts)
		}
	}
}

func assertError(t testing.TB, err error, want string) {
    t.Helper()
    if err == nil {
        t.Fatalf("Expected Error %q", want)
    }
    assertString(t, err.Error(), want)
}

func assertString(t testing.TB, got, want string) {
    t.Helper()
    if got != want {
        t.Errorf("got:\n%q\nwant:\n%q", got, want)
    }
}