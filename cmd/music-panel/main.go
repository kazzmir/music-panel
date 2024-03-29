package main

import (
    // "time"
    "io/ioutil"
    "os"
    "os/signal"
    "os/exec"
    "syscall"
    "sort"
    "sync"
    "flag"
    _ "embed"
    "bytes"
    "strings"
    "strconv"
    "log"
    "fmt"
    "context"
    "time"
    "path/filepath"

    "gopkg.in/yaml.v2"
    "github.com/mattn/go-gtk/gtk"
)

//go:embed on.png
var PngOn []byte
//go:embed off.png
var PngOff []byte

func GetOrCreateConfigDir() (string, error) {
    configDir, err := os.UserConfigDir()
    if err != nil {
        return "", err
    }
    configPath := filepath.Join(configDir, "music-panel")
    err = os.MkdirAll(configPath, 0755)
    if err != nil {
        return "", err
    }

    return configPath, nil
}

type Config struct {
    /* map from a name to a url */
    Urls map[string]string
}

func (config *Config) AllItems() []string {
    var out []string = nil
    for key := range config.Urls {
        out = append(out, key)
    }
    return out
}

func (config *Config) GetUrl(name string) string {
    url, ok := config.Urls[name]
    if ok {
        return url
    }

    return ""
}

func loadConfig(path string) (Config, error) {
    config := Config{
        Urls: make(map[string]string),
    }

    data, err := ioutil.ReadFile(path)
    if err != nil {
        return config, err
    }

    var info map[string]interface{}

    err = yaml.Unmarshal(data, &info)
    if err != nil {
        return config, err
    }

    /* don't need the key */
    for _, values := range info {
        // log.Printf("Read %v = %v", xname, values)
        valueData, ok := values.(map[interface{}]interface{})
        if ok {
            name, ok := valueData["name"]
            if !ok {
                log.Printf("Didn't get a name")
                continue
            }
            url, ok := valueData["url"]
            if !ok {
                log.Printf("Didn't get a url")
                continue
            }

            config.Urls[name.(string)] = url.(string)
        }
    }

    return config, nil
}

type ProgramAction interface {
}

type ProgramActionStop struct {
}

type ProgramActionRestart struct {
}

type ProgramActionPlay struct {
    Name string
}

func sortedStrings(data []string) []string {
    sort.Sort(sort.StringSlice(data))
    return data
}

const NoMusic string = "no-music"

func makePopup(config Config, currentSong string, actions chan ProgramAction) *gtk.Menu {
    menu := gtk.NewMenu()
    noMusic := NoMusic

    items := []string{noMusic}
    items = append(items, sortedStrings(config.AllItems())...)

    for _, name := range items {
        realName := name
        if name == noMusic {
            realName = "None"
        }

        make_click := func(name string) func() {
            return func(){
                if name == noMusic {
                    actions <- &ProgramActionStop{}
                } else {
                    actions <- &ProgramActionPlay{
                        Name: name,
                    }
                }
            }
        }

        if name == currentSong {
            /* lame that this logic can't be re-used */
            item := gtk.NewCheckMenuItemWithLabel(realName)
            item.SetActive(true)
            item.Connect("activate", make_click(name))
            item.Show()
            menu.Append(item)
        } else {
            item := gtk.NewMenuItemWithLabel(realName)
            item.Connect("activate", make_click(name))
            item.Show()
            menu.Append(item)
        }
    }

    return menu
}

type RemovePidCallback func()

func saveMplayerPid(command *exec.Cmd) RemovePidCallback {

    if command.Process != nil {
        pid := command.Process.Pid

        dir, err := GetOrCreateConfigDir()
        if err == nil {
            now := time.Now()
            file := filepath.Join(dir, fmt.Sprintf("mplayer-%v.pid", now.UnixNano()))
            err := os.WriteFile(file, []byte(strconv.Itoa(pid)), 0600)

            if err == nil {
                return func(){
                    os.Remove(file)
                }
            }
        }
    }

    return func(){
    }
}

/* saves the url as the last station listened to */
func updateLastStation(name string) error {
    dir, err := GetOrCreateConfigDir()
    if err != nil {
        return err
    }

    return os.WriteFile(filepath.Join(dir, "last"), []byte(name), 0600)
}

func readLastName() (string, bool) {
    dir, err := GetOrCreateConfigDir()
    if err != nil {
        return "", false
    }

    data, err := os.ReadFile(filepath.Join(dir, "last"))
    if err != nil {
        return "", false
    }

    return string(data), true
}

func SaveTempPng(data []byte) string {
    path, err := os.CreateTemp("", "music-panel*.png")
    if err != nil {
        out := "/tmp/music-panel-tmp.png"
        err := os.WriteFile(out, data, 0600)
        if err != nil {
            // not sure what to do
            return ""
        }
        return out
    }

    path.Write(data)

    return path.Name()
}

func doLoadConfig() (Config, error) {
    configPath := "config.yml"
    config, err := loadConfig(configPath)
    if err == nil {
        log.Printf("Loaded '%v'", configPath)
        return config, nil
    }

    configDir, err := GetOrCreateConfigDir()
    if err == nil {
        full := filepath.Join(configDir, configPath)
        config, err = loadConfig(full)
        if err == nil {
            log.Printf("Loaded '%v'", full)
            return config, nil
        }
    }

    return Config{}, err
}

func run(globalQuit context.Context, globalCancel context.CancelFunc, wait *sync.WaitGroup, autoRun bool){
    defer globalCancel()

    config, err := doLoadConfig()
    if err != nil {
        log.Printf("Could not load config file: %v", err)
        return
    }

    actions := make(chan ProgramAction, 10)

    currentPlaying := NoMusic
    path := SaveTempPng(PngOff)
    icon := gtk.NewStatusIconFromFile(path)
    os.Remove(path)
    icon.Connect("activate", func() {
        menu := makePopup(config, currentPlaying, actions)
        menu.Popup(nil, nil, nil, nil, 0, gtk.GetCurrentEventTime())
    })
    icon.SetTooltipText("Not playing")
    icon.SetVisible(true)

    if autoRun {
        lastUrl, ok := readLastName()
        if ok {
            actions <- &ProgramActionPlay{
                Name: lastUrl,
            }
        }
    }

    doPlay := func(name string, url string) (context.Context, context.CancelFunc) {
        wait.Add(1)
        quit, cancel := context.WithCancel(globalQuit)
        /* a command context will send SIGKILL if the context is cancelled,
         * so we use a separate context to deal with the process
         */
        killQuit, killCancel := context.WithCancel(context.Background())
        /* FIXME: be able to run with ffmpeg, gstreamer, maybe some other players */
        command := exec.CommandContext(killQuit, "mplayer", "-prefer-ipv4", "-loop", "0", url)
        command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
        err := command.Start()

        if err != nil {
            log.Printf("Could not start playing '%v': %v", name, err)
        } else {
            log.Printf("Launched mplayer with pid %v\n", command.Process.Pid)
        }

        doRemovePid := saveMplayerPid(command)

        updateLastStation(name)

        /* automatically shut off the stream after 24 hours, so that it doesn't
         * accidentally play forever
         */
        go func(){
            select {
                case <-quit.Done():
                case <-time.After(24 * time.Hour):
                    cancel()
            }
        }()

        go func(){
            <-quit.Done()
            // command.Process.Signal(syscall.SIGTERM)
            syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
            time.Sleep(2 * time.Second)
            // make sure the process dies
            killCancel()
        }()

        go func(){
            command.Wait()
            log.Printf("Mplayer command stopped %v", command.Process.Pid)
            cancel()
            doRemovePid()
            wait.Done()
        }()

        return quit, cancel
    }

    go func(){
        /* sleep for 1 second and check if roughly 1 second has elapsed. if more than 1 second has gone by
         * then it is likely that the computer went to sleep via suspend/hibernate, so mplayer should be restarted
         */
        for globalQuit.Err() == nil {
            start := time.Now()
            time.Sleep(time.Second)
            end := time.Now()
            if (end.Sub(start) > time.Second * 2){
                /* try to emit the restart event, but ignore it if actions is full */
                select {
                    case actions <- &ProgramActionRestart{}:
                    default:
                }
            }
        }
    }()

    go func(){
        playQuit, playCancel := context.WithCancel(globalQuit)
        _ = playQuit

        defer playCancel()

        for {
            select {
                case <-globalQuit.Done():
                    return
                case <-playQuit.Done():
                    actions <- &ProgramActionStop{}
                    log.Printf("Music stopped")
                case action := <-actions:
                    _, ok := action.(*ProgramActionStop)
                    if ok {
                        log.Printf("Stop playing")
                        currentPlaying = NoMusic
                        path := SaveTempPng(PngOff)
                        icon.SetFromFile(path)
                        os.Remove(path)
                        icon.SetTooltipText("Not playing")
                        playCancel()
                        playQuit, playCancel = context.WithCancel(context.Background())
                    }

                    _, ok = action.(*ProgramActionRestart)
                    if ok {
                        if currentPlaying != NoMusic {
                            log.Printf("Restarting stream")
                            url := config.GetUrl(currentPlaying)
                            if url != "" {
                                log.Printf("Play url '%v' = '%v'", currentPlaying, url)

                                path := SaveTempPng(PngOn)
                                icon.SetFromFile(path)
                                os.Remove(path)
                                icon.SetTooltipText(fmt.Sprintf("Playing '%v'", currentPlaying))

                                playCancel()
                                playQuit, playCancel = doPlay(currentPlaying, url)
                            }
                        }
                    }

                    play, ok := action.(*ProgramActionPlay)
                    if ok {
                        url := config.GetUrl(play.Name)
                        if url != "" {
                            log.Printf("Play url '%v' = '%v'", play.Name, url)
                            /* FIXME: race condition */
                            currentPlaying = play.Name

                            path := SaveTempPng(PngOn)
                            icon.SetFromFile(path)
                            os.Remove(path)
                            icon.SetTooltipText(fmt.Sprintf("Playing '%v'", play.Name))

                            playCancel()
                            playQuit, playCancel = doPlay(play.Name, url)
                        }
                    }
            }
        }
    }()

    gtk.Main()

    log.Printf("Main done")
}

func runPsPid(pid int) (string, error) {
    command := exec.Command("ps", "-o", "cmd", strconv.Itoa(pid))
    var out strings.Builder
    command.Stdout = &out
    err := command.Run()
    return out.String(), err
}

/* /proc/$pid/cmdline contains the program and its arguments as it was executed where each
 * string value is separated by a null (0) byte
 */
func readProcName(pid int) (string, error) {
    data, err := os.ReadFile(fmt.Sprintf("/proc/%v/cmdline", pid))
    if err != nil {
        return "", err
    }

    /* the command should be the first thing in the output that ends with an null byte */
    before, _, found := bytes.Cut(data, []byte{0})
    if found {
        return string(before), nil
    }

    return "", fmt.Errorf("unable to parse cmdline")
}

func readProcExe(pid int) (string, error) {
    link, err := filepath.EvalSymlinks(fmt.Sprintf("/proc/%v/exe", pid))
    if err != nil {
        return "", err
    }
    return link, nil
}

/* ps output is like
 * CMD
 * xyz arg1 arg2 ...
 */
func extractPsProcess(output string) (string, bool) {
    lines := strings.Split(output, "\n")
    if len(lines) >= 2 {
        use := lines[1]
        parts := strings.Fields(use)
        if len(parts) > 0 {
            return parts[0], true
        }
    }

    return "", false
}

func checkProcessName(processName string, pid int) bool {
    /* check that the given pid has the right processName using one of three methods:
     * 1. read /proc/$pid/cmdline and compare the first string to processName
     * 2. follow the /proc/$pid/exe symlink and check the binary has the processName
     * 3. use 'ps -o cmd $pid' to see what the command is
     */

    procName, err := readProcName(pid)
    if err == nil {
        return procName == processName
    }
    
    procExe, err := readProcExe(pid)
    if err == nil {
        // procExe is a path to some binary on the system
        return filepath.Base(procExe) == processName
    }

    psOutput, err := runPsPid(pid)
    if err == nil {
        psName, ok := extractPsProcess(psOutput)
        if ok {
            return psName == processName
        }
    }

    return false
}

/* kill a pid but only if it has the given process name */
func maybeKillPid(processName string, pid int){
    // first check the process even exists
    process, err := os.FindProcess(pid)
    if err != nil {
        return
    }

    if checkProcessName(processName, pid) {
        log.Printf("Killing leftover mplayer process %v", pid)

        _ = process

        /* FIXME: check process name by looking at /proc/$pid/cmdline */

        syscall.Kill(-pid, syscall.SIGTERM)
    }
}

func killExistingMplayer(){
    dir, err := GetOrCreateConfigDir()
    if err == nil {
        paths, err := os.ReadDir(dir)
        if err == nil {
            for _, path := range paths {
                if path.IsDir() {
                    continue
                }

                fullPath := filepath.Join(dir, path.Name())

                if strings.HasPrefix(path.Name(), "mplayer") && strings.HasSuffix(path.Name(), ".pid") {
                    contents, err := os.ReadFile(fullPath)
                    if err == nil {
                        pid, err := strconv.Atoi(string(contents))
                        if err == nil {
                            maybeKillPid("mplayer", pid)
                        }
                    }

                    os.Remove(fullPath)
                }
            }
        } else {
            log.Printf("Could not get list of old files: %v", err)
        }
    }
}

func fixTty(){
    /* run 'stty sane' */
    /* only need this if we show the output of mplayer */
}

func main(){
    log.SetFlags(log.Lshortfile | log.Ldate | log.Lmicroseconds)
    log.Printf("Initializing")
    gtk.Init(&os.Args)

    autoRun := flag.Bool("auto-run", false, "automatically run the last played station")
    flag.Parse()

    globalQuit, globalCancel := context.WithCancel(context.Background())

    signaler := make(chan os.Signal, 10)
    signal.Notify(signaler, syscall.SIGINT, syscall.SIGTERM)

    killExistingMplayer()

    var wait sync.WaitGroup

    go func(){
        /* let the user press ctrl-c once to cleanly stop, and twice to hard kill */
        for count := 0; count < 2; count += 1 {
            select {
                case <-signaler:
                    log.Printf("Caught signal, shutting down")
                    globalCancel()
                    gtk.MainQuit()
            }
        }

        log.Printf("Hard shutdown")
        os.Exit(1)
    }()

    run(globalQuit, globalCancel, &wait, *autoRun)

    <-globalQuit.Done()
    wait.Wait()

    fixTty()
    log.Printf("Goodbye")
}
