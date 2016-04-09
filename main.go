package main

import (
	"crypto/sha1"
	"crypto/tls"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"flag"

	"github.com/AlexanderThaller/logger"
	"github.com/gorilla/mux"
	"github.com/juju/errgo"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	folder = "files"
	Name   = "uploader"
)

var (
	FlagSecretUser     string
	FlagSecretPassword string

	uploads = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "uploader",
		Subsystem: "newfiles",
		Name:      "uploads",
		Help:      "The number of uploads made in the application",
	})
	downloads = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "uploader",
		Subsystem: "newfiles",
		Name:      "downloads",
		Help:      "The number of downloads made by the application",
	})
	sent = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "uploader",
		Subsystem: "sent",
		Name:      "files",
		Help:      "The count of sent files",
	})
	uploadsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "uploader",
		Subsystem: "newfiles",
		Name:      "uploads_active",
		Help:      "The count of currently active uploads",
	})
	downloadsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "uploader",
		Subsystem: "newfiles",
		Name:      "downloads_active",
		Help:      "The count of currently active downloads",
	})
)

func init() {
	logger.SetLevel(".", logger.Trace)
	flag.StringVar(&FlagSecretUser, "secret.user", "", "username needed for uploading files")
	flag.StringVar(&FlagSecretPassword, "secret.password", "", "password needed for uploading files")

	prometheus.MustRegister(uploads)
	prometheus.MustRegister(downloads)
	prometheus.MustRegister(sent)
	prometheus.MustRegister(uploadsActive)
	prometheus.MustRegister(downloadsActive)
}

func main() {
	l := logger.New(Name, "main")
	flag.Parse()

	router := mux.NewRouter()

	if FlagSecretUser != "" && FlagSecretPassword != "" {
		router.HandleFunc("/", auth(root))
		router.HandleFunc("/upload", auth(upload))
		router.HandleFunc("/upload/", auth(upload))
		router.HandleFunc("/download", auth(download))
		router.HandleFunc("/download/", auth(download))
	} else {
		router.HandleFunc("/", root)
		router.HandleFunc("/upload", upload)
		router.HandleFunc("/upload/", upload)
		router.HandleFunc("/download", download)
		router.HandleFunc("/download/", download)
	}

	router.HandleFunc("/files/{hash}/{filename}", files)
	router.HandleFunc("/files/{hash}/{filename}/", files)
	router.HandleFunc("/loading/{timestamp}", downloadStatus)
	router.HandleFunc("/loading/{timestamp}/", downloadStatus)

	http.Handle("/metrics", prometheus.Handler())
	http.Handle("/", router)

	l.Notice("Listening on 10443")
	err := http.ListenAndServe("localhost:10443", nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func root(w http.ResponseWriter, req *http.Request) {
	p := `<html><title>Uploader</title><body>
      <h1>Upload a file</h1>
      <form action="/upload" method="post" enctype="multipart/form-data">
        <input name="file" type="file" size="50" maxlength="100000000">
        <br><br>
        <input type="submit" value="Upload" />
      </form>

      <hr>

      <h1>Download from an URL</h1>
      <form action="/download" method="post" novalidate>
        <input name="url" type="text" size="50">
        <br><br>
        <input type="submit" value="Download" />
      </form>
      </body></html>`

	fmt.Fprintf(w, p)
}

func upload(w http.ResponseWriter, req *http.Request) {
	l := logger.New(Name, "upload")
	l.Debug("New upload")
	uploadsActive.Inc()

	file, handler, err := req.FormFile("file")
	if err != nil {
		fmt.Fprintln(w, "Problem when getting file: ", err)
		return
	}
	l.Notice("Receiving file: " + handler.Filename)

	data, err := ioutil.ReadAll(file)
	if err != nil {
		fmt.Fprintln(w, "Problem when reading: ", err)
		return
	}

	sum := fmt.Sprintf("%x", sha1.Sum(data))
	outpath := path.Join(folder, sum)

	err = os.MkdirAll(outpath, 0750)
	if err != nil {
		fmt.Fprintln(w, "Problem when mking dir: ", err)
		return
	}

	outfile := path.Join(outpath, handler.Filename)

	err = ioutil.WriteFile(outfile, data, 0640)
	if err != nil {
		fmt.Fprintln(w, "Problem when writing file: ", err)
		return
	}
	var proto = req.Header.Get("X-Forwarded-Proto")

	if proto == "" {
		proto = "http"
	}

	fmt.Fprintln(w, proto+"://"+req.Host+"/"+outfile)
	l.Notice("Request: ", fmt.Sprintf("%+v", req))
	l.Notice("Saved file: " + outfile)

	uploadsActive.Dec()
	uploads.Inc()
}

func files(w http.ResponseWriter, req *http.Request) {
	l := logger.New("files")

	vars := mux.Vars(req)
	hash := vars["hash"]
	filename := vars["filename"]

	filepath := path.Join("files", hash, filename)
	l.Notice("Serving: ", filepath)

	http.ServeFile(w, req, filepath)
	sent.Inc()
}

func download(w http.ResponseWriter, req *http.Request) {
	downloadsActive.Inc()

	linkurl, err := url.Parse(req.FormValue("url"))
	if err != nil {
		fmt.Fprintln(w, "can not parse url: ", errgo.Details(err))
		return
	}

	timestamp := time.Now()
	downloadLog(timestamp, "Downloading: ", linkurl)
	go loader(linkurl, timestamp)

	filename := strconv.FormatInt(timestamp.UnixNano(), 10)
	http.Redirect(w, req, "/loading/"+filename, 303)

	downloads.Inc()
}

func downloadLog(timestamp time.Time, message ...interface{}) {
	filename := strconv.FormatInt(timestamp.UnixNano(), 10)
	l := logger.New(Name, "downloadLog", filename)

	dir := filepath.Join("files", "tmp", filename)
	filepath := path.Join(dir, "log")

	err := os.MkdirAll(dir, 0750)
	if err != nil {
		l.Error("can not create dir: ", errgo.Details(err))
		return
	}

	file, err := os.OpenFile(filepath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		l.Error("Can not open logfile: ", errgo.Details(err))
		return
	}
	defer file.Close()

	fmt.Fprintln(file, time.Now(), " -- ", message)
}

func loader(linkurl *url.URL, timestamp time.Time) {
	filename := strconv.FormatInt(timestamp.UnixNano(), 10)
	l := logger.New(Name, "loader", filename)

	l.Info("Downloading: ", linkurl)

	dir := filepath.Join("files", "tmp", filename)

	err := os.MkdirAll(dir, 0750)
	if err != nil {
		l.Error("can not create dir: ", errgo.Details(err))
		downloadLog(timestamp, "can not create dir: ", err)
		return
	}

	path := path.Join(dir, "file")

	out, err := os.Create(path)
	if err != nil {
		l.Error("can not create tmp file: ", errgo.Details(err))
		downloadLog(timestamp, "can not create tmp file: ", err)
		return
	}
	defer out.Close()

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	client := &http.Client{Transport: tr}
	resp, err := client.Get(linkurl.String())
	if err != nil {
		l.Error("can not get from url: ", errgo.Details(err))
		downloadLog(timestamp, "can not get from url: ", err)
		return
	}
	defer resp.Body.Close()

	_, err = io.Copy(out, resp.Body)
	l.Info("Finished loading ", linkurl)
	downloadLog(timestamp, "Finished loading ", linkurl)

	go hasher(linkurl, timestamp)
}

func hasher(linkurl *url.URL, timestamp time.Time) {
	filename := strconv.FormatInt(timestamp.UnixNano(), 10)
	l := logger.New(Name, "hasher", filename)

	l.Info("Hashing ", linkurl)
	downloadLog(timestamp, "Hashing ", linkurl)

	dir := filepath.Join("files", "tmp", filename)
	path := filepath.Join(dir, "file")

	file, err := os.OpenFile(path, os.O_RDONLY, 0640)
	if err != nil {
		l.Error("Can not open file: ", errgo.Details(err))
		downloadLog(timestamp, "Can not open file ", err)
		return
	}
	defer file.Close()

	hasher := sha1.New()
	_, err = io.Copy(hasher, file)
	if err != nil {
		l.Error("can not copy file to hasher: ", errgo.Details(err))
		downloadLog(timestamp, "can not copy file to hasher: ", err)
		return
	}

	hash := fmt.Sprintf("%x", hasher.Sum(nil))
	l.Info("Finished hashing, hash is", hash)
	downloadLog(timestamp, "Finished hashing, hash is", hash)

	go mover(linkurl, timestamp, hash)
}

func mover(linkurl *url.URL, timestamp time.Time, hash string) {
	filename := strconv.FormatInt(timestamp.UnixNano(), 10)
	l := logger.New(Name, "mover", filename)

	dir := filepath.Join("files", "tmp", filename)
	path := filepath.Join(dir, "file")
	destdir := filepath.Join("files", hash)

	escapedurl := strings.Replace(linkurl.String(), ":", "-", -1)
	escapedurl = strings.Replace(escapedurl, "/", "_", -1)
	escapedurl = url.QueryEscape(escapedurl)

	destination := filepath.Join(destdir, escapedurl)

	l.Info("Moving to ", destination)
	downloadLog(timestamp, "Moving to ", destination)

	err := os.MkdirAll(destdir, 0750)
	if err != nil {
		l.Error("can not create destination dir: ", errgo.Details(err))
		downloadLog(timestamp, "can not create desintaion dir: ", err)
		return
	}

	err = os.Rename(path, destination)
	if err != nil {
		l.Error("can not move file to destination", errgo.Details(err))
		downloadLog(timestamp, "can not move file to destination", err)
		return
	}

	statuspath := filepath.Join(dir, "done")
	err = ioutil.WriteFile(statuspath, []byte(destination), 0640)
	if err != nil {
		l.Warning("can not write status file: ", err)
		return
	}

	l.Info("Finished")
	downloadLog(timestamp, "Finished")
	downloadsActive.Dec()
}

func downloadStatus(w http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	filename := vars["timestamp"]
	l := logger.New(Name, "downloadStatus", filename)

	dir := filepath.Join("files", "tmp", filename)

	statuspath := filepath.Join(dir, "done")

	newpath, err := ioutil.ReadFile(statuspath)
	if err == nil {
		http.Redirect(w, req, "/"+string(newpath), 301)
		return
	}

	filepath := filepath.Join(dir, "log")

	log, err := ioutil.ReadFile(filepath)
	if err != nil {
		l.Warning("can not open logfile: ", errgo.Details(err))
		fmt.Fprintf(w, "can not open logfile: ", err)
	}

	w.Write(log)
}
