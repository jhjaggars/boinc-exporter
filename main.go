package main

import (
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var clientStatePath string = "/var/lib/boinc-client/client_state.xml"

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

var boinc_active_task_fraction_done = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "boinc_active_task_fraction_done",
	Help: "percentage of task completed",
}, []string{"name"})

var boinc_active_task_elapsed_time = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "boinc_active_task_elapsed_time",
	Help: "time spent working on active task",
}, []string{"name"})

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
		for _, task := range state.ActiveTaskSet.ActiveTasks {
			boinc_active_task_elapsed_time.WithLabelValues(task.Name).Set(task.ElapsedTime)
			boinc_active_task_fraction_done.WithLabelValues(task.Name).Set(task.FractionDone)
		}
		next.ServeHTTP(w, r)
	})
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
	http.ListenAndServe(fmt.Sprintf(":%s", port), nil)
}
