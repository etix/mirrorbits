package http

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/etix/mirrorbits/database"
	"github.com/gomodule/redigo/redis"
)

type Metrics struct {
	metricsResponse string
	lock            sync.Mutex
}

type metricsUnit struct {
	Day   int
	Month int
	Year  int
	Total int
}

// NewMetrics returns a new instance of metrics
func NewMetrics(r *database.Redis) *Metrics {
	metrics := Metrics{
		metricsResponse: "",
	}
	go func() {
		for {
			metrics.getMetrics(r)
			time.Sleep(2 * time.Minute)
		}
	}()
	return &metrics
}

func statsToPrometheusFormat(metrics metricsUnit, labelName string, labelValue string) string {
	var output string

	output += fmt.Sprintf("%s_total{%s=\"%s\"} %d\n", labelName, labelName, labelValue, metrics.Total)
	output += fmt.Sprintf("%s_day{%s=\"%s\"} %d\n", labelName, labelName, labelValue, metrics.Day)
	output += fmt.Sprintf("%s_month{%s=\"%s\"} %d\n", labelName, labelName, labelValue, metrics.Month)
	output += fmt.Sprintf("%s_year{%s=\"%s\"} %d\n\n", labelName, labelName, labelValue, metrics.Year)

	return output
}

func (m *Metrics) getMetrics(httpRedis *database.Redis) {
	rconn := httpRedis.Get()
	defer rconn.Close()

	// Get all mirrors ID
	mirrorsMap, err := httpRedis.GetListOfMirrors()
	if err != nil {
		log.Error("Cannot fetch the list of mirrors: " + err.Error())
		return
	}

	var mirrorsIDs []int
	for id := range mirrorsMap {
		// We need a common order to iterate the
		// results from Redis.
		mirrorsIDs = append(mirrorsIDs, id)
	}

	rconn.Send("MULTI")

	today := time.Now()
	for _, id := range mirrorsIDs {
		rconn.Send("HGET", "STATS_MIRROR", id)
		rconn.Send("HGET", "STATS_MIRROR_"+today.Format("2006_01_02"), id)
		rconn.Send("HGET", "STATS_MIRROR_"+today.Format("2006_01"), id)
		rconn.Send("HGET", "STATS_MIRROR_"+today.Format("2006"), id)
	}

	stats, err := redis.Values(rconn.Do("EXEC"))
	if err != nil {
		log.Error("Cannot fetch stats: " + err.Error())
		return
	}

	if len(stats) == 0 {
		log.Info("Metrics: no files")
		return
	}

	var index int64
	var output string
	for _, id := range mirrorsIDs {
		var mirror metricsUnit
		mirror.Total, _ = redis.Int(stats[index], err)
		mirror.Day, _ = redis.Int(stats[index+1], err)
		mirror.Month, _ = redis.Int(stats[index+2], err)
		mirror.Year, _ = redis.Int(stats[index+3], err)
		output += statsToPrometheusFormat(mirror, "mirror", mirrorsMap[id])
		index += 4
	}

	// Get all files
	var fileList []string
	fileList, err = httpRedis.GetListOfFiles()
	if err != nil {
		log.Error("Cannot fetch list of files: " + err.Error())
		return
	}

	rconn.Send("MULTI")
	for _, file := range fileList {
		rconn.Send("HGET", "STATS_FILE", file)
		rconn.Send("HGET", "STATS_FILE_"+today.Format("2006_01_02"), file)
		rconn.Send("HGET", "STATS_FILE_"+today.Format("2006_01"), file)
		rconn.Send("HGET", "STATS_FILE_"+today.Format("2006"), file)
	}

	stats, err = redis.Values(rconn.Do("EXEC"))
	if err != nil {
		log.Error("Cannot fetch stats: " + err.Error())
		return
	}

	index = 0
	for _, name := range fileList {
		var metrics metricsUnit
		metrics.Total, _ = redis.Int(stats[index], err)
		metrics.Day, _ = redis.Int(stats[index+1], err)
		metrics.Month, _ = redis.Int(stats[index+2], err)
		metrics.Year, _ = redis.Int(stats[index+3], err)
		output += statsToPrometheusFormat(metrics, "file", name)
		index += 4
	}

	// Get all countries
	var countryList []string
	countryList, err = httpRedis.GetListOfCountries()
	if err != nil {
		log.Error("Cannot fetch list of countries: " + err.Error())
		return
	}

	rconn.Send("MULTI")
	for _, country := range countryList {
		rconn.Send("HGET", "STATS_COUNTRY", country)
		rconn.Send("HGET", "STATS_COUNTRY_"+today.Format("2006_01_02"), country)
		rconn.Send("HGET", "STATS_COUNTRY_"+today.Format("2006_01"), country)
		rconn.Send("HGET", "STATS_COUNTRY_"+today.Format("2006"), country)
	}
	stats, err = redis.Values(rconn.Do("EXEC"))
	if err != nil {
		log.Error("Cannot fetch stats: " + err.Error())
		return
	}

	index = 0
	for _, name := range countryList {
		var metrics metricsUnit
		metrics.Total, _ = redis.Int(stats[index], err)
		metrics.Day, _ = redis.Int(stats[index+1], err)
		metrics.Month, _ = redis.Int(stats[index+2], err)
		metrics.Year, _ = redis.Int(stats[index+3], err)
		output += statsToPrometheusFormat(metrics, "country", name)
		index += 4
	}
	m.lock.Lock()
	m.metricsResponse = output
	m.lock.Unlock()
	return
}

func (h *HTTP) metricsHandler(w http.ResponseWriter, r *http.Request) {
	h.metrics.lock.Lock()
	output := h.metrics.metricsResponse
	h.metrics.lock.Unlock()
	w.Write([]byte(output))
}
