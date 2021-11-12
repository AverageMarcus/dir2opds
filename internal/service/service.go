//package service provides a http handler that reads the path in the request.url and returns
// an xml document that follows the OPDS 1.1 standard
// https://specs.opds.io/opds-1.1.html
package service

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io/fs"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dubyte/dir2opds/opds"
)

func init() {
	_ = mime.AddExtensionType(".mobi", "application/x-mobipocket-ebook")
	_ = mime.AddExtensionType(".epub", "application/epub+zip")
	_ = mime.AddExtensionType(".cbz", "application/x-cbz")
	_ = mime.AddExtensionType(".cbr", "application/x-cbr")
	_ = mime.AddExtensionType(".fb2", "text/fb2+xml")
	_ = mime.AddExtensionType(".pdf", "application/pdf")
}

const (
	pathTypeFile = iota
	pathTypeDirOfDirs
	pathTypeDirOfFiles
)

var files = []BookFile{}

type BookFile struct {
	Name     string
	Path     string
	Author   string
	FileInfo fs.FileInfo
}

type OPDS struct {
	DirRoot     string
	Author      string
	AuthorEmail string
	AuthorURI   string
}

var TimeNow = timeNowFunc()

const navigationType = "application/atom+xml;profile=opds-catalog;kind=navigation"

// Handler serve the content of a book file or
// returns an Acquisition Feed when the entries are documents or
// returns an Navegation Feed when the entries are other folders
func (s OPDS) Handler(w http.ResponseWriter, req *http.Request) error {
	var err error
	urlPath, err := url.PathUnescape(req.URL.Path)
	if err != nil {
		log.Printf("error while serving '%s': %s", req.URL.Path, err)
		return err
	}
	fPath := filepath.Join(s.DirRoot, urlPath)

	log.Printf("urlPath:'%s'", urlPath)
	log.Printf("fPath:'%s'", fPath)

	feedBuilder := opds.FeedBuilder.
		ID(urlPath).
		Title(strings.Title(strings.TrimPrefix(urlPath, "/"))).
		Author(opds.AuthorBuilder.Name(s.Author).Email(s.AuthorEmail).URI(s.AuthorURI).Build()).
		Updated(TimeNow()).
		AddLink(opds.LinkBuilder.Rel("start").Href("/").Type(navigationType).Build())

	if urlPath == "/" {
		files = []BookFile{}
		filepath.WalkDir(s.DirRoot, func(path string, de fs.DirEntry, err error) error {
			if !de.IsDir() {
				file, err := de.Info()
				if err != nil {
					fmt.Println(err)
					return nil
				}

				files = append(files, BookFile{
					Name:     file.Name(),
					Path:     path,
					FileInfo: file,
				})
			}
			return nil
		})

		feedBuilder = feedBuilder.
			AddEntry(opds.EntryBuilder.
				ID("/latest").
				Title("Latest").
				Updated(TimeNow()).
				Published(TimeNow()).
				AddLink(opds.LinkBuilder.Rel(getRel("latest", pathTypeDirOfDirs)).Title("Latest").Href(filepath.Join("/", url.PathEscape("latest"))).Type(getType("Latest", pathTypeDirOfDirs)).Build()).
				Build()).
			AddEntry(opds.EntryBuilder.
				ID("/titles").
				Title("By Title").
				Updated(TimeNow()).
				Published(TimeNow()).
				AddLink(opds.LinkBuilder.Rel(getRel("titles", pathTypeDirOfDirs)).Title("By Title").Href(filepath.Join("/", url.PathEscape("titles"))).Type(getType("By Title", pathTypeDirOfDirs)).Build()).
				Build())
	} else if urlPath == "/latest" {
		fPath = strings.TrimSuffix(fPath, "/latest")
		for _, f := range sortByLatest(files) {
			fi := f.FileInfo
			pathType := getPathType(f.Path)
			feedBuilder = feedBuilder.
				AddEntry(opds.EntryBuilder.
					ID(urlPath + fi.Name()).
					Title(fi.Name()).
					Updated(TimeNow()).
					Published(TimeNow()).
					AddLink(opds.LinkBuilder.Rel(getRel(f.Path, pathType)).Title(fi.Name()).Href(filepath.Join("/", url.PathEscape(strings.TrimPrefix(f.Path, s.DirRoot)))).Type(getType(f.Path, pathType)).Build()).
					Build())
		}
	} else if urlPath == "/titles" {
		fPath = strings.TrimSuffix(fPath, "/titles")
		for _, f := range sortByTitle(files) {
			fi := f.FileInfo
			pathType := getPathType(f.Path)
			feedBuilder = feedBuilder.
				AddEntry(opds.EntryBuilder.
					ID(urlPath + fi.Name()).
					Title(fi.Name()).
					Updated(TimeNow()).
					Published(TimeNow()).
					AddLink(opds.LinkBuilder.Rel(getRel(f.Path, pathType)).Title(fi.Name()).Href(filepath.Join("/", url.PathEscape(strings.TrimPrefix(f.Path, s.DirRoot)))).Type(getType(f.Path, pathType)).Build()).
					Build())
		}
	} else if getPathType(fPath) == pathTypeFile {
		http.ServeFile(w, req, fPath)
		return nil
	}

	navFeed := feedBuilder.Build()

	var content []byte
	if getPathType(fPath) == pathTypeDirOfFiles {
		acFeed := &opds.AcquisitionFeed{Feed: &navFeed, Dc: "http://purl.org/dc/terms/", Opds: "http://opds-spec.org/2010/catalog"}
		content, err = xml.MarshalIndent(acFeed, "  ", "    ")
		w.Header().Add("Content-Type", "application/atom+xml;profile=opds-catalog;kind=acquisition")
	} else {
		content, err = xml.MarshalIndent(navFeed, "  ", "    ")
		w.Header().Add("Content-Type", "application/atom+xml;profile=opds-catalog;kind=navigation")
	}
	if err != nil {
		log.Printf("error while serving '%s': %s", fPath, err)
		return err
	}

	content = append([]byte(xml.Header), content...)
	http.ServeContent(w, req, "feed.xml", TimeNow(), bytes.NewReader(content))

	return nil
}

func getRel(name string, pathType int) string {
	if pathType == pathTypeDirOfFiles || pathType == pathTypeDirOfDirs {
		return "subsection"
	}

	ext := filepath.Ext(name)
	if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" {
		return "http://opds-spec.org/image/thumbnail"
	}

	// mobi, epub, etc
	return "http://opds-spec.org/acquisition"
}

func getType(name string, pathType int) string {
	switch pathType {
	case pathTypeFile:
		return mime.TypeByExtension(filepath.Ext(name))
	case pathTypeDirOfFiles:
		return "application/atom+xml;profile=opds-catalog;kind=acquisition"
	case pathTypeDirOfDirs:
		return "application/atom+xml;profile=opds-catalog;kind=navigation"
	default:
		return mime.TypeByExtension("xml")
	}
}

func getPathType(dirpath string) int {
	fi, _ := os.Stat(dirpath)
	if isFile(fi) {
		return pathTypeFile
	}

	fis, _ := ioutil.ReadDir(dirpath)
	for _, fi := range fis {
		if isFile(fi) {
			return pathTypeDirOfFiles
		}
	}
	// Directory of directories
	return pathTypeDirOfDirs
}

func isFile(fi os.FileInfo) bool {
	return !fi.IsDir()
}

func timeNowFunc() func() time.Time {
	t := time.Now()
	return func() time.Time { return t }
}

func sortByLatest(files []BookFile) []BookFile {
	sortedFiles := files
	sort.Slice(sortedFiles, func(i, j int) bool {
		return sortedFiles[i].FileInfo.ModTime().After(sortedFiles[j].FileInfo.ModTime())
	})

	return sortedFiles
}

func sortByTitle(files []BookFile) []BookFile {
	sortedFiles := files
	sort.Slice(sortedFiles, func(i, j int) bool {
		return strings.Compare(sortedFiles[i].Name, sortedFiles[j].Name) < 0
	})

	return sortedFiles
}
