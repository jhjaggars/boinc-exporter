package main

import (
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/nxadm/tail"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var clientStatePath string = "/var/lib/boinc-client/client_state.xml"
var hostname string = "localhost"

var boinc_hostinfo_domainname = promauto.NewSummary(prometheus.SummaryOpts{
	Name: "boinc_hostinfo_domainname",
	Help: "Name of the boinc client domain",
})

var boinc_result_deadline = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "boinc_result_deadline",
	Help: "unix time to deadline",
}, []string{"name"})

var boinc_result_received_time = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "boinc_result_received_time",
	Help: "unix time received",
}, []string{"name"})

var boinc_active_task_count = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "boinc_active_task_count",
	Help: "current number of tasks",
})

var boinc_active_task_fraction_done = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "boinc_active_task_fraction_done",
	Help: "percentage of task completed",
}, []string{"name"})

var boinc_active_task_elapsed_time = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "boinc_active_task_elapsed_time",
	Help: "time spent working on active task",
}, []string{"name"})

var boinc_task_assigned = promauto.NewCounter(prometheus.CounterOpts{
	Name: "boinc_task_assigned",
	Help: "task assignments",
})

var boinc_task_started = promauto.NewCounter(prometheus.CounterOpts{
	Name: "boinc_task_started",
	Help: "task starting",
})

var boinc_task_completed = promauto.NewCounter(prometheus.CounterOpts{
	Name: "boinc_task_completed",
	Help: "task completed",
})

var boinc_task_uploaded = promauto.NewCounter(prometheus.CounterOpts{
	Name: "boinc_task_uploaded",
	Help: "task uploaded",
})

var boinc_task_downloaded = promauto.NewCounter(prometheus.CounterOpts{
	Name: "boinc_task_downloaded",
	Help: "task downloaded",
})

type HostInfo struct {
	DomainName string `xml:"domain_name"`
}

type Result struct {
	Name           string  `xml:"name"`
	ReportDeadline float64 `xml:"report_deadline"`
	ReceivedTime   float64 `xml:"received_time"`
	VersionNumber  uint16  `xml:"version_num"`
}

type ActiveTaskSet struct {
	ActiveTasks []ActiveTask `xml:"active_task"`
}

type ActiveTask struct {
	Name         string  `xml:"result_name"`
	FractionDone float64 `xml:"checkpoint_fraction_done"`
	ElapsedTime  float64 `xml:"checkpoint_elapsed_time"`
}

type ClientState struct {
	HostInfo      HostInfo      `xml:"host_info"`
	Results       []Result      `xml:"result"`
	ActiveTaskSet ActiveTaskSet `xml:"active_task_set"`
}

func fetch(state *ClientState) error {
	bytes, err := os.ReadFile(clientStatePath)
	if err != nil {
		return err
	}

	if err := xml.Unmarshal(bytes, &state); err != nil {
		return err
	}
	return nil
}

func syncMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var state ClientState
		if err := fetch(&state); err != nil {
			log.Fatalf("failed to fetch: %s", err)
		}
		// fmt.Printf("domain_name = %s\n", state.HostInfo.DomainName)
		for _, result := range state.Results {
			boinc_result_deadline.WithLabelValues(result.Name).Set(result.ReportDeadline)
			boinc_result_received_time.WithLabelValues(result.Name).Set(result.ReportDeadline)
		}
		boinc_active_task_count.Set(float64(len(state.ActiveTaskSet.ActiveTasks)))
		for _, task := range state.ActiveTaskSet.ActiveTasks {
			boinc_active_task_elapsed_time.WithLabelValues(task.Name).Set(task.ElapsedTime)
			boinc_active_task_fraction_done.WithLabelValues(task.Name).Set(task.FractionDone)
		}
		next.ServeHTTP(w, r)
	})
}

// Scheduler request completed: got 0 new tasks
var intRe = regexp.MustCompile(`\d+`)

type addFunc func(line string) (int, error)

func getInt(line string) (int, error) {
	return strconv.Atoi(intRe.FindString(line))
}

func addOne(_ string) (int, error) {
	return 1, nil
}

func addIntFrom(line, sub string, counter prometheus.Counter, adder addFunc) bool {
	if idx := strings.Index(line, sub); idx > -1 {
		num, err := adder(line[idx:])
		if err != nil {
			log.Printf("expected integer in substring '%s', but didn't find one: %s", sub, err.Error())
			return true
		}
		counter.Add(float64(num))
		return true
	}
	return false
}

func logLineParse(line string) {

	if addIntFrom(line, "Scheduler request complete: got", boinc_task_assigned, getInt) {
		return
	}
	if addIntFrom(line, "Starting task", boinc_task_started, addOne) {
		return
	}
	if addIntFrom(line, "Computation for task", boinc_task_completed, addOne) {
		return
	}
	if addIntFrom(line, "Finished upload of", boinc_task_uploaded, addOne) {
		return
	}
	if addIntFrom(line, "Finished download of", boinc_task_downloaded, addOne) {
		return
	}
}

func follow(logfilePath string) {
	t, err := tail.TailFile(logfilePath, tail.Config{
		Follow: true,
		ReOpen: true,
	})
	if err != nil {
		log.Printf("failed to tail logfile, no metrics will be collected.: %s", err.Error())
	}
	for line := range t.Lines {
		logLineParse(line.Text)
	}
}

func main() {
	path := os.Getenv("BOINC_CLIENT_STATE_XML")
	if path != "" {
		clientStatePath = path
	}
	httpPath := os.Getenv("METRICS_HTTP_PATH")
	if httpPath == "" {
		httpPath = "/metrics"
	}
	http.Handle(httpPath, syncMiddleware(promhttp.Handler()))
	port := os.Getenv("METRICS_HTTP_PORT")
	if port == "" {
		port = "9100"
	}
	logfilePath := os.Getenv("BOINC_LOGFILE_PATH")
	if logfilePath != "" {
		go follow(logfilePath)
	}
	log.Printf("boinc-exporter starting on :%s", port)
	err := http.ListenAndServe(fmt.Sprintf(":%s", port), nil)
	if err != nil {
		log.Printf("http server failed: %s", err.Error())
	}
}
