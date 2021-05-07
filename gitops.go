package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go"
)

type gitOps struct {
	basePath        string
	cachePath       string
	upToDate        bool
	errored         bool
	followHierarchy bool
	cache           map[string]interface{}
	gitURL          string
	cacheFileName   string
	verbose         bool
}

func (g *gitOps) init(url string) {
	g.basePath = os.Getenv("DA_GIT_REPOS_PATH")
	if g.basePath == "" {
		g.basePath = os.Getenv("HOME") + "/.perceval/repositories"
	}
	g.upToDate = false
	g.errored = false
	g.followHierarchy = false
	g.cache = make(map[string]interface{})
	g.gitURL = g.getProcessedURI(url)
	g.cacheFileName = "stats.json"
	g.verbose = os.Getenv("GITOPS_VERBOSE") != ""
}

func (g *gitOps) getProcessedURI(uri string) string {
	removal := ".git"
	if strings.HasSuffix(uri, removal) {
		return uri[:len(uri)-4]
	}
	return uri
}

func (g *gitOps) cachePathFunc() string {
	g.cachePath = os.Getenv("DA_GIT_CACHE_PATH")
	if g.cachePath == "" {
		g.cachePath = os.Getenv("HOME") + "/.perceval/cache"
	}
	_, err := os.Stat(g.cachePath)
	if os.IsNotExist(err) {
		_ = os.MkdirAll(g.cachePath, 0777)
	}
	return g.cachePath
}

func (g *gitOps) sanitizeURL(path string) string {
	if strings.HasPrefix(path, "/r/") {
		path = strings.Replace(path, "/r/", "", 1)
	} else if strings.HasPrefix(path, "/gerrit/") {
		path = strings.Replace(path, "/gerrit/", "", 1)
	}
	return strings.TrimLeft(path, "/")
}

func (g *gitOps) buildOrgName(path string, gitSource bool) string {
	sanitizePath := g.sanitizeURL(path)
	if !gitSource {
		ary := strings.Split(sanitizePath, ".")
		return ary[1]
	}
	ary := strings.Split(sanitizePath, "/")
	return ary[0]
}

func (g *gitOps) buildRepoName(path, orgName string) string {
	sanitizePath := g.sanitizeURL(path)
	if strings.Contains(sanitizePath, orgName) {
		sanitizePath = strings.Replace(sanitizePath, orgName+"/", "", 1)
	}
	if !g.followHierarchy {
		sanitizePath = strings.Replace(sanitizePath, "/", "-", 1)
		sanitizePath = strings.Replace(sanitizePath, "_", "-", 1)
		sanitizePath = strings.Replace(sanitizePath, "/.", "", 1)
		sanitizePath = strings.Replace(sanitizePath, ".", "", 1)
	}
	return sanitizePath
}

func (g *gitOps) isGitSource(host string) bool {
	if strings.Contains(host, "github.com") || strings.Contains(host, "gitlab.com") || strings.Contains(host, "bitbucket.org") {
		return true
	}
	return false
}

func (g *gitOps) orgName() string {
	parser, err := url.Parse(g.gitURL)
	if err != nil {
		panic(err)
	}
	if g.isGitSource(parser.Host) {
		return g.buildOrgName(parser.Path, true)
	}
	return g.buildOrgName(parser.Host, false)
}

func (g *gitOps) repoName() string {
	parser, err := url.Parse(g.gitURL)
	if err != nil {
		panic(err)
	}
	return g.buildRepoName(parser.Path, g.orgName())
}

func (g *gitOps) getCachePath() string {
	basePath := g.cachePathFunc()
	path := path.Join(basePath, g.orgName())
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		_ = os.MkdirAll(path, 0777)
	}
	return path
}

func (g *gitOps) buildEmptyStatsData() map[string]interface{} {
	statsData := make(map[string]interface{})
	r := make(map[string]interface{})
	r["loc"] = 0
	r["pls"] = []interface{}{}
	r["timestamp"] = nil
	statsData[g.repoName()] = r
	return statsData
}

func (g *gitOps) writeJSONFile(data map[string]interface{}, filePath, fileName string) {
	path := path.Join(filePath, fileName)
	fileData, _ := jsoniter.MarshalIndent(data, "", " ")
	err := ioutil.WriteFile(path, fileData, 0666)
	if err != nil {
		fmt.Printf("cannot write JSON object to %s: %+v\n", path, err)
		g.errored = true
	}
}

func (g *gitOps) readJSONFile(filePath, fileName string) map[string]interface{} {
	path := path.Join(filePath, fileName)
	bts, err := ioutil.ReadFile(path)
	if err != nil {
		fmt.Printf("cannot read JSON file %s: %+v\n", path, err)
		// g.errored = true
		return g.buildEmptyStatsData()
	}
	var obj map[string]interface{}
	err = jsoniter.Unmarshal(bts, &obj)
	if err != nil {
		fmt.Printf("cannot unmarshal JSON file %s: %+v\n", path, err)
		// g.errored = true
		return g.buildEmptyStatsData()
	}
	return obj
}

func (g *gitOps) loadCache() {
	path := path.Join(g.getCachePath(), g.cacheFileName)
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		statsData := g.buildEmptyStatsData()
		g.cache = statsData
		g.writeJSONFile(statsData, g.getCachePath(), g.cacheFileName)
	} else {
		g.cache = g.readJSONFile(g.getCachePath(), g.cacheFileName)
		_, ok := g.cache[g.repoName()]
		if !ok {
			statsData := g.buildEmptyStatsData()
			g.cache[g.repoName()] = statsData[g.repoName()]
			g.writeJSONFile(g.cache, g.getCachePath(), g.cacheFileName)
		}
	}
	if g.verbose {
		fmt.Printf("%s: %+v\n", path, g.cache)
	}
}

func (g *gitOps) repoPath() string {
	if g.followHierarchy {
		return path.Join(g.basePath, g.orgName()+"/"+g.repoName())
	}
	return path.Join(g.basePath, g.orgName()+"-"+g.repoName())
}

func (g *gitOps) clone() {
	args := []string{"git", "clone", g.gitURL, g.repoPath()}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "LANG=C", "HOME="+os.Getenv("HOME"))
	err := cmd.Run()
	if err != nil {
		fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
		g.errored = true
	}
	if g.verbose {
		fmt.Printf("executed %+v\n", args)
	}
}

func (g *gitOps) clean(force bool) {
	sizeBytes := g.getRepoSize(g.repoPath())
	size := g.getSizeFormat(float64(sizeBytes), 1024.0, "B")
	if g.shouldBeDeleted(size) || force {
		args := []string{"rm", "-rf", g.repoPath()}
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Env = append(os.Environ(), "LANG=C", "HOME="+os.Getenv("HOME"))
		err := cmd.Run()
		if err != nil {
			fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
		}
		if g.verbose {
			fmt.Printf("executed %+v\n", args)
		}
	}
}

func (g *gitOps) fetch() {
	path, err := filepath.Abs(g.repoPath())
	if err != nil {
		fmt.Printf("error absolute path %s: %+v\n", path, err)
		return
	}
	err = os.Chdir(path)
	if err != nil {
		fmt.Printf("error chdir to %s: %+v\n", path, err)
		return
	}
	args := []string{"git", "fetch"}
	cmd := exec.Command(args[0], args[1:]...)
	env := append(os.Environ(), "LANG=C", "HOME="+os.Getenv("HOME"))
	cmd.Env = env
	err = cmd.Run()
	if err != nil {
		fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
		g.errored = true
	}
	if g.verbose {
		fmt.Printf("executed %+v\n", args)
	}
	args = append(args, "-p")
	cmd = exec.Command(args[0], args[1:]...)
	cmd.Env = env
	err = cmd.Run()
	if err != nil {
		fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
		g.errored = true
	}
	if g.verbose {
		fmt.Printf("executed %+v\n", args)
	}
}

func (g *gitOps) pull() bool {
	path, err := filepath.Abs(g.repoPath())
	if err != nil {
		fmt.Printf("error absolute path %s: %+v\n", path, err)
		return false
	}
	err = os.Chdir(path)
	if err != nil {
		fmt.Printf("error chdir to %s: %+v\n", path, err)
		return false
	}
	var (
		status bool
		outb   bytes.Buffer
		branch string
	)
	args := []string{"git", "remote", "set-head", "origin", "--auto"}
	cmd := exec.Command(args[0], args[1:]...)
	env := append(os.Environ(), "LANG=C", "HOME="+os.Getenv("HOME"))
	cmd.Env = env
	err = cmd.Run()
	if err != nil {
		fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
		g.errored = true
	}
	if g.verbose {
		fmt.Printf("executed %+v\n", args)
	}
	args = []string{"git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD"}
	cmd = exec.Command(args[0], args[1:]...)
	cmd.Env = env
	cmd.Stdout = &outb
	err = cmd.Run()
	if err != nil {
		fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
		g.errored = true
	} else {
		result := outb.String()
		branch = strings.TrimSpace(strings.Replace(result, "origin/", "", 1))
	}
	if g.verbose {
		fmt.Printf("executed %+v, got %s branch\n", args, branch)
	}
	if branch != "" {
		args = []string{"git", "checkout", branch}
		cmd = exec.Command(args[0], args[1:]...)
		cmd.Env = env
		cmd.Stdout = nil
		err = cmd.Run()
		if err != nil {
			fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
			g.errored = true
		}
		if g.verbose {
			fmt.Printf("executed %+v\n", args)
		}
		var ob bytes.Buffer
		args = []string{"git", "pull", "origin", branch}
		cmd = exec.Command(args[0], args[1:]...)
		cmd.Env = env
		cmd.Stdout = &ob
		err = cmd.Run()
		if err != nil {
			fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
			g.errored = true
		} else {
			result := ob.String()
			if strings.Contains(result, "Already up to date.") {
				status = true
			}
			if g.verbose {
				fmt.Printf("executed %+v, got %s -> %v\n", args, result, status)
			}
		}
	}
	return status
}

func (g *gitOps) loc(value string) int64 {
	var locValue int64
	if g.verbose {
		defer func() {
			fmt.Printf("loc %s ---> %d\n", value, locValue)
		}()
	}
	if strings.Contains(value, "SUM:") || strings.Contains(value, "Language") {
		ary := strings.Split(value, "\n")
		lAry := len(ary)
		if lAry >= 3 {
			ary2 := strings.Split(ary[lAry-3], " ")
			locValue, _ = strconv.ParseInt(ary2[len(ary2)-1], 10, 64)
		}
	}
	return locValue
}

func (g *gitOps) pls(value string) []map[string]interface{} {
	var stats []map[string]interface{}
	if g.verbose {
		defer func() {
			fmt.Printf("pls %s ---> %+v\n", value, stats)
		}()
	}
	if strings.Contains(value, "SUM:") || strings.Contains(value, "Language") {
		lanSmryLst := strings.Split(value, "\n")
		nLanSmryLst := len(lanSmryLst)
		var hasLanguage bool = false
		for i := 0; i <= nLanSmryLst-1; i++ {
			smry := lanSmryLst[i]
			if strings.HasPrefix(smry, "---") {
				continue
			}
			if strings.HasPrefix(smry, "Language") {
				hasLanguage = true
				continue
			}
			if hasLanguage {
				smryResult := strings.Fields(smry)
				stats = append(stats, map[string]interface{}{
					"language": strings.Replace(smryResult[0], "SUM:", "Total", -1),
					"files":    smryResult[1],
					"blank":    smryResult[2],
					"comment":  smryResult[3],
					"code":     smryResult[4],
				})
			}
		}
	}
	return stats
}

func (g *gitOps) stats(path string) string {
	var (
		ob     bytes.Buffer
		result string
	)
	env := append(os.Environ(), "LANG=C", "HOME="+os.Getenv("HOME"))
	args := []string{"cloc", path}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = env
	cmd.Stdout = &ob
	err := cmd.Run()
	if err != nil {
		fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
	} else {
		result = ob.String()
		if g.verbose {
			fmt.Printf("executed %+v, got %s\n", args, result)
		}
	}
	return result
}

func (g *gitOps) getCacheItem(projectName, key string) interface{} {
	iProj, ok := g.cache[projectName]
	if !ok {
		return nil
	}
	proj, ok := iProj.(map[string]interface{})
	if !ok {
		return nil
	}
	obj, _ := proj[key]
	return obj
}

func (g *gitOps) updateCacheItem(projectName, key string, value interface{}) {
	iData, ok := g.cache[projectName]
	if ok {
		data, ok := iData.(map[string]interface{})
		if ok {
			data[key] = value
			g.cache[projectName] = data
		}
	}
}

func (g *gitOps) getStats() (int64, []map[string]interface{}) {
	var (
		loc      int64
		cacheLOC int64
		pls      []map[string]interface{}
		cachePLS []map[string]interface{}
	)
	iCacheLOC := g.getCacheItem(g.repoName(), "loc")
	cacheLOC, _ = iCacheLOC.(int64)
	iCachePLS := g.getCacheItem(g.repoName(), "pls")
	aCachePLS, _ := iCachePLS.([]interface{})
	for _, item := range aCachePLS {
		iface, _ := item.(map[string]interface{})
		cachePLS = append(cachePLS, iface)
	}
	result := g.stats(g.repoPath())
	loc = g.loc(result)
	pls = g.pls(result)
	if loc == 0 {
		loc = cacheLOC
		pls = cachePLS
	} else {
		g.updateCacheItem(g.repoName(), "loc", loc)
		g.updateCacheItem(g.repoName(), "pls", pls)
		utcDate := time.Now().UTC().Format(time.RFC3339)
		g.updateCacheItem(g.repoName(), "timestamp", utcDate)
		g.writeJSONFile(g.cache, g.getCachePath(), g.cacheFileName)
	}
	return loc, pls
	// Set cache_loc value if loc operations failed
	// loc = cacheLOC
	// pls = cacheLOC
}

func (g *gitOps) getRepoSize(startPath string) int64 {
	var siz int64
	err := filepath.Walk(startPath,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink == 0 {
				siz += info.Size()
			}
			return nil
		})
	if err != nil {
		fmt.Printf("error walking file path %s: %+v\n", startPath, err)
	}
	return siz
}

func (g *gitOps) getSizeFormat(sizeBytes, factor float64, suffix string) string {
	for _, unit := range []string{"", "K", "M", "G", "T", "P", "E", "Z"} {
		if sizeBytes < factor {
			return fmt.Sprintf("%.2f %s%s", sizeBytes, unit, suffix)
		}
		sizeBytes /= factor
	}
	return fmt.Sprintf("%.2f Y%s", sizeBytes, suffix)
}

func (g *gitOps) shouldBeDeleted(sizeUnit string) bool {
	if sizeUnit != "" {
		ary := strings.Split(sizeUnit, " ")
		if len(ary) < 2 {
			return false
		}
		unit := ary[1]
		if unit == "B" || unit == "KB" {
			return true
		} else if unit == "MB" {
			size := ary[0]
			fSize, err := strconv.ParseFloat(size, 64)
			if err == nil && fSize <= 200.0 {
				return true
			}
		}
	}
	return false
}

func (g *gitOps) load() {
	_, err := os.Stat(g.repoPath())
	if os.IsNotExist(err) {
		g.clone()
	} else {
		g.fetch()
		g.upToDate = g.pull()
	}
}

func (g *gitOps) isErrored() bool {
	return g.errored
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(1)
		return
	}
	gitops := gitOps{}
	gitops.init(os.Args[1])
	gitops.loadCache()
	gitops.load()
	loc, pls := gitops.getStats()
	if os.Getenv("SKIP_CLEANUP") == "" {
		gitops.clean(false)
	}
	if gitops.isErrored() {
		os.Exit(1)
	}
	obj := map[string]interface{}{"loc": loc, "pls": pls}
	data, _ := jsoniter.Marshal(obj)
	fmt.Printf("%s\n", string(data))
	if gitops.verbose {
		fmt.Printf("repo path: %s\ncache path: %s\n", gitops.repoPath(), gitops.getCachePath())
	}
}
