package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// creates a new object of Foreman
func new() *Foreman {
	foreman := Foreman {
		procfile: procfile,
		signalsChannel: make(chan os.Signal, MaxSizeChannel),
		servicesToRunChannel: make(chan string, MaxNumServices),
		checksTicker: time.NewTicker(TickInterval),
		services: map[string]Service{},
		servicesGraph: map[string][]string{},
	}
	return &foreman
}

// initForeman initialises a new foreman object and signals, then parses the procfile
// and then builds the dependency graph
func initForeman() *Foreman {
	foreman := new()
	foreman.signal()
	if err := foreman.parseProcfile(); err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	foreman.buildServicesGraph()

	return foreman
}

// runServices runs services after sorting them topologically by first creating
// a pool of workers threads that recieve services from servicesToRunChannel.
// It spawns a periodic checker thread that run services checks after constant
// duration.
// If the grapth has cycles, the program aborts and prints an error message.
func (foreman *Foreman) runServices() {
	if cycleExist, parentMap := graphHasCycle(foreman.servicesGraph); cycleExist {
		cycleElementsList := getCycleElements(parentMap)
		fmt.Printf("found cycle please fix: [%v]\n", strings.Join(cycleElementsList, ", "))
		os.Exit(1)
	}

	topologicallySortedServices := foreman.topoSortServices()
	
	

	foreman.createServiceRunners(foreman.servicesToRunChannel, NumWorkersThreads)
	sendServicesOnChannel(topologicallySortedServices, foreman.servicesToRunChannel)

	foreman.runPeriodicChecker(foreman.checksTicker)
}

// createServiceRunners creates a worker pool by starting up numWorkers workers threads
func (foreman *Foreman) createServiceRunners(services <-chan string, numWorkers int) {
	for w := 0; w < numWorkers; w++ {
		go foreman.serviceRunner(services)
	}
}

// serviceRunner is the worker, of which we’ll run several concurrent instances.
func (foreman *Foreman) serviceRunner(services <-chan string) {
	for serviceName := range services {
		foreman.runService(serviceName)
	}
}

// serviceDepsAreAllActive checks if the all dependences of a service are active.
func (foreman *Foreman) serviceDepsAreAllActive(service Service) (bool, string) {
	for _, dep := range service.info.deps {
		if foreman.services[dep].status == inactive {
			foreman.restartService(dep)
			return false, dep
		} 
	}
	return true, ""
}

// runService run service by spawning a new process for this service.
// the new spawned process has a new process group id equals its pid.
func (foreman *Foreman) runService(serviceName string) {
	service := foreman.services[serviceName]
	if (len(service.info.cmd)) > 0 {
		if ok, _ := foreman.serviceDepsAreAllActive(service); ok {
			serviceCmd := exec.Command("bash", "-c", service.info.cmd)
			serviceCmd.SysProcAttr = &syscall.SysProcAttr{
				Setpgid: true,
				Pgid: 0,
			}
			serviceCmd.Start()
			service.status = active
			service.pid = serviceCmd.Process.Pid
			fmt.Printf("[%d] %s process started [%v]\n", service.pid, service.name, time.Now())
			foreman.services[serviceName] = service
		} else {
			foreman.restartService(serviceName)
		}
	}
}

// sendServicesOnChannel helper function sends a list of services to a service channel.
func sendServicesOnChannel(servicesList []string, servicesChannel chan<- string) {
	for _, service := range servicesList {
		servicesChannel <- service
	}
}

// runPeriodicChecker runs a new foreman thread at every tick from the ticker.
func (foreman *Foreman) runPeriodicChecker(ticker *time.Ticker) {
	for range ticker.C {
		go foreman.checker()
	}
}

// checker the checker process that runs all the checks of all services.
func (foreman *Foreman) checker() {
	for _, service := range foreman.services {
		if service.status == active {
			foreman.runServiceChecks(service)
		}
	}
}

// runServiceChecks helper function runs the checks of a service.
func (foreman *Foreman) runServiceChecks(service Service) {
	if service.status == active {
		if ok, dep := foreman.serviceDepsAreAllActive(service); !ok {
			if err := syscall.Kill(service.pid, syscall.SIGTERM); err != nil {
				syscall.Kill(service.pid, syscall.SIGKILL)
				return
			}
			fmt.Printf("[%d] %s process terminated as dependency [%q] check failed\n", service.pid, service.name, dep)
			return
		}
		if service.status == active && len(service.info.checks.cmd) > 0 {
			
			checkCmd := exec.Command("bash", "-c", service.info.checks.cmd)
	
			if err := checkCmd.Run(); err != nil {
				if err := syscall.Kill(service.pid, syscall.SIGTERM); err != nil {
					syscall.Kill(service.pid, syscall.SIGKILL)
					return
				}
				fmt.Printf("[%d] %s process terminated as check [%v] failed\n", service.pid, service.name, service.info.checks.cmd)
				return
			}
		}
		if service.status == active && len(service.info.checks.tcpPorts) > 0 {
			for _, port := range service.info.checks.tcpPorts {
				cmd := fmt.Sprintf("netstat -lnptu | grep tcp | grep %s -m 1 | awk '{print $7}'", port)
				out, _ := exec.Command("bash", "-c", cmd).Output()
				pid, err := strconv.Atoi(strings.Split(string(out), "/")[0])
				if err != nil || pid != service.pid {
					if err := syscall.Kill(service.pid, syscall.SIGTERM); err != nil {
						syscall.Kill(service.pid, syscall.SIGKILL)
						return
					}
					fmt.Printf("[%d] %s process terminated, as TCP port [%v] is not listening\n", service.pid, service.name, port)
					return
				}
			}
		}
	
		if service.status == active && len(service.info.checks.udpPorts) > 0 {
			for _, port := range service.info.checks.udpPorts {
				cmd := fmt.Sprintf("netstat -lnptu | grep udp | grep %s -m 1 | awk '{print $7}'", port)
				out, _ := exec.Command("bash", "-c", cmd).Output()
				pid, err := strconv.Atoi(strings.Split(string(out), "/")[0])
				if err != nil || pid != service.pid {
					if syscall.Kill(service.pid, syscall.SIGTERM); err != nil {
						syscall.Kill(service.pid, syscall.SIGKILL)
						return
					}
					fmt.Printf("[%d] %s process terminated, as UDP port [%v] is not listening\n", service.pid, service.name, port)
					return
				}
			}
		}
	}
}