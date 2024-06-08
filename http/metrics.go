package http

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/database"
	"github.com/gomodule/redigo/redis"
)

type Metrics struct {
	metricsResponse string
	lock            sync.Mutex
	trackedFileList []string
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
			trackedFiles, err := r.GetListOfTrackedFiles()
			if err != nil {
				log.Error("WTF")
				log.Error(err.Error)
			} else {
				metrics.trackedFileList = trackedFiles
			}
			time.Sleep(2 * time.Minute)
		}
	}()
	return &metrics
}

func (m *Metrics) IsFileTracked(file string) (bool, error) {
	for _, v := range m.trackedFileList {
		if v == file {
			return true, nil
		}
	}
	return false, nil
}

func statsToPrometheusFormat(metrics metricsUnit, labelName string, labelValue string) string {
	var output string

	output += fmt.Sprintf("%s_total{%s=\"%s\"} %d\n", labelName, labelName, labelValue, metrics.Total)
	output += fmt.Sprintf("%s_day{%s=\"%s\"} %d\n", labelName, labelName, labelValue, metrics.Day)
	output += fmt.Sprintf("%s_month{%s=\"%s\"} %d\n", labelName, labelName, labelValue, metrics.Month)
	output += fmt.Sprintf("%s_year{%s=\"%s\"} %d\n\n", labelName, labelName, labelValue, metrics.Year)

	return output
}

func getSimpleMetrics(rconn redis.Conn, fields []string, name string, prefix string) (string, error) {
	rconn.Send("MULTI")
	today := time.Now()
	for _, field := range fields {
		rconn.Send("HGET", prefix, field)
		rconn.Send("HGET", prefix+"_"+today.Format("2006_01_02"), field)
		rconn.Send("HGET", prefix+"_"+today.Format("2006_01"), field)
		rconn.Send("HGET", prefix+"_"+today.Format("2006"), field)
	}

	stats, err := redis.Values(rconn.Do("EXEC"))
	if err != nil {
		return "", err
	}

	index := 0
	output := ""
	for _, field := range fields {
		var metrics metricsUnit
		metrics.Total, _ = redis.Int(stats[index], err)
		metrics.Day, _ = redis.Int(stats[index+1], err)
		metrics.Month, _ = redis.Int(stats[index+2], err)
		metrics.Year, _ = redis.Int(stats[index+3], err)
		output += statsToPrometheusFormat(metrics, name, field)
		index += 4
	}

	return output, nil
}

func (m *Metrics) getMetrics(httpRedis *database.Redis) {
	rconn := httpRedis.Get()
	defer rconn.Close()

	// Get all mirrors
	// The output will be similar to:
	// mirror_total{mirror="ftp-mirror"} 0
	// mirror_day{mirror="ftp-mirror"} 0
	// mirror_month{mirror="ftp-mirror"} 0
	// mirror_year{mirror="ftp-mirror"} 0
	//
	// mirror_total{mirror="rsync-mirror"} 1046
	// mirror_day{mirror="rsync-mirror"} 12
	// mirror_month{mirror="rsync-mirror"} 1046
	// mirror_year{mirror="rsync-mirror"} 1046

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
	// The output will be similar to:
	// file_total{file="/file_1.txt"} 538
	// file_day{file="/file_1.txt"} 9
	// file_month{file="/file_1.txt"} 538
	// file_year{file="/file_1.txt"} 538
	//
	// file_total{file="/file_2.txt"} 508
	// file_day{file="/file_2.txt"} 3
	// file_month{file="/file_2.txt"} 508
	// file_year{file="/file_2.txt"} 508

	var fileList []string
	fileList, err = httpRedis.GetListOfFiles()
	if err != nil {
		log.Error("Cannot fetch list of files: " + err.Error())
		return
	}

	fileOutput, err := getSimpleMetrics(rconn, fileList, "file", "STATS_FILE")
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
	output += fileOutput

	// Get all countries
	// The output will be similar to:
	// country_total{country="France"} 501
	// country_day{country="France"} 7
	// country_month{country="France"} 501
	// country_year{country="France"} 501
	//
	// country_total{country="United States"} 545
	// country_day{country="United States"} 5
	// country_month{country="United States"} 545
	// country_year{country="United States"} 545
	var countryList []string
	countryList, err = httpRedis.GetListOfCountries()
	if err != nil {
		log.Error("Cannot fetch list of countries: " + err.Error())
		return
	}

	countryOutput, err := getSimpleMetrics(rconn, countryList, "country", "STATS_COUNTRY")
	if err != nil {
		log.Error("Cannot fetch stats: " + err.Error())
		return
	}
	output += countryOutput

	// Get all tracked files download counts

	// Each STATS_TRACKED_* hash has the following format:
	// n) Country_MirrorID
	// n+1) Download count for the combination mirror / country

	// So that there is no interruption in the count timeseries, every time
	// a new country_mirrorid pair is introduced, it must export a value
	// even if there is no database hash for the current day.
	// Hence we have to get all fields in the current STATS_TRACKED_* hashes
	// and complete with fields that were in previous hashes but set to 0.
	// For this, fields in STATS_TRACKED_filename without any date (the total)
	// can be used

	// The output will be similar to:
	// stats_tracked_{file="/file_1.txt",country="France",mirror="rsync-mirror"} 14
	// stats_tracked_{file="/file_1.txt",country="United States",mirror="rsync-mirror"} 11
	//
	// stats_tracked_day{file="/file_1.txt",country="France",mirror="rsync-mirror"} 14
	// stats_tracked_day{file="/file_1.txt",country="United States",mirror="rsync-mirror"} 11
	//
	// stats_tracked_month{file="/file_1.txt",country="France",mirror="rsync-mirror"} 14
	// stats_tracked_month{file="/file_1.txt",country="United States",mirror="rsync-mirror"} 11
	//
	// stats_tracked_year{file="/file_1.txt",country="France",mirror="rsync-mirror"} 14
	// stats_tracked_year{file="/file_1.txt",country="United States",mirror="rsync-mirror"} 11
	//
	// stats_top{file="/file_1.txt",country="France",mirror="rsync-mirror"} 14
	// stats_top{file="/file_1.txt",country="United States",mirror="rsync-mirror"} 11

	mkeyList := make([]string, 0)
	mkeyFileMap := make(map[string]string)
	fileFieldsMap := make(map[string][]string)

	// Get tracked files downloads per country and mirror
	for _, file := range m.trackedFileList {
		mkey := "STATS_TRACKED_" + file + "_" + today.Format("2006_01_02")
		// Getting all Country_MirrorID fields for that file
		fileFieldsMap[file], err = httpRedis.GetListOfTrackFilesFields(file)
		if err != nil {
			log.Error("Failed to fetch file fields: ", err.Error())
			return
		}
		for i := 0; i < 4; i++ {
			mkeyList = append(mkeyList, mkey)
			mkeyFileMap[mkey] = file
			mkey = mkey[:strings.LastIndex(mkey, "_")]
		}
	}

	rconn.Send("MULTI")
	for _, mkey := range mkeyList {
		rconn.Send("HGETALL", mkey)
	}
	stats, err = redis.Values(rconn.Do("EXEC"))
	if err != nil {
		log.Error("Cannot fetch files per country stats: " + err.Error())
		return
	}

	index = 0
	// Get all download count information from the previous redis querries
	for _, mkey := range mkeyList {
		hashSlice, _ := redis.ByteSlices(stats[index], err)
		file := mkeyFileMap[mkey]
		fieldList := make([]string, len(fileFieldsMap[file]))
		copy(fieldList, fileFieldsMap[file])
		sort.Strings(fieldList)
		// This loop gets the download count from the database
		for i := 0; i < len(hashSlice); i += 2 {
			field, _ := redis.String(hashSlice[i], err)
			value, _ := redis.Int(hashSlice[i+1], err)
			sep := strings.Index(field, "_")
			country := field[:sep]
			mirrorID, err := strconv.Atoi(field[sep+1:])
			if err != nil {
				log.Error("Failed to convert mirror ID: ", err)
				return
			}
			mirror := mirrorsMap[mirrorID]
			durationStr := getDurationFromKey(mkey)
			output += fmt.Sprintf("stats_tracked_"+
				"%s{file=\"%s\",country=\"%s\",mirror=\"%s\"} %d\n",
				durationStr, mkeyFileMap[mkey], country, mirror, value,
			)
			fieldIndex := sort.SearchStrings(fieldList, field)
			if fieldIndex < len(fieldList) {
				fieldList = append(fieldList[:fieldIndex], fieldList[fieldIndex+1:]...)
			}
		}
		// This loop set the download count to 0 as their is no current entry
		// in the database of downloads by these mirrors and countries
		for _, field := range fieldList {
			sep := strings.Index(field, "_")
			country := field[:sep]
			mirrorID, err := strconv.Atoi(field[sep+1:])
			if err != nil {
				log.Error("Failed to convert mirror ID: ", err)
				return
			}
			mirror := mirrorsMap[mirrorID]
			durationStr := getDurationFromKey(mkey)
			output += fmt.Sprintf("stats_tracked_"+
				"%s{file=\"%s\",country=\"%s\",mirror=\"%s\"} %d\n",
				durationStr, mkeyFileMap[mkey], country, mirror, 0,
			)
		}
		index++
		output += "\n"
	}

	// Get daily stats for top 10
	retention := config.GetConfig().MetricsTopFilesRetention
	for nbDays := 0; nbDays < retention; nbDays++ {
		day := today.AddDate(0, 0, nbDays*-1)
		rconn.Send("MULTI")
		for _, file := range fileList {
			mkey := fmt.Sprintf("STATS_TOP_%s_%s", file,
				day.Format("2006_01_02"))
			rconn.Send("HGETALL", mkey)
		}
		stats, err = redis.Values(rconn.Do("EXEC"))
		if err != nil {
			log.Error("Cannot fetch file per country stats: " + err.Error())
			return
		}
		for index, stat := range stats {
			hashSlice, _ := redis.ByteSlices(stat, err)
			for i := 0; i < len(hashSlice); i += 2 {
				field, _ := redis.String(hashSlice[i], err)
				value, _ := redis.Int(hashSlice[i+1], err)
				sep := strings.Index(field, "_")
				country := field[:sep]
				mirrorID, err := strconv.Atoi(field[sep+1:])
				if err != nil {
					log.Error("Failed to convert mirror ID: ", err)
					return
				}
				mirror := mirrorsMap[mirrorID]
				output += fmt.Sprintf("stats_top"+
					"{file=\"%s\",country=\"%s\",mirror=\"%s\"} %d\n",
					fileList[index], country, mirror, value,
				)
			}
		}
	}

	m.lock.Lock()
	m.metricsResponse = output
	m.lock.Unlock()
	return
}

func (h *HTTP) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if config.GetConfig().MetricsEnabled {
		h.metrics.lock.Lock()
		output := h.metrics.metricsResponse
		h.metrics.lock.Unlock()
		w.Write([]byte(output))
		log.Debug("test")
	} else {
		log.Errorf("Error: metrics are disabled")
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
	}

}

func getDurationFromKey(key string) string {
	dayPattern := regexp.MustCompile("([0-9]{4}_[0-9]{2}_[0-9]{2})\\b")
	monthPattern := regexp.MustCompile("([0-9]{4}_[0-9]{2})\\b")
	yearPattern := regexp.MustCompile("([0-9]{4})\\b")
	var durationStr string
	if len(dayPattern.FindAllStringIndex(key, -1)) > 0 {
		durationStr = "day"
	} else if len(monthPattern.FindAllStringIndex(key, -1)) > 0 {
		durationStr = "month"
	} else if len(yearPattern.FindAllStringIndex(key, -1)) > 0 {
		durationStr = "year"
	} else {
		durationStr = "total"
	}
	return durationStr
}
