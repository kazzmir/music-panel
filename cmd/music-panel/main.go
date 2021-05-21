package main

import (
    // "time"
    "io/ioutil"
    "os"
    "os/signal"
    "os/exec"
    "syscall"
    "sort"
    "log"
    "fmt"
    "context"
    "time"

    "gopkg.in/yaml.v2"
    "github.com/mattn/go-gtk/gtk"
)

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

func run(globalQuit context.Context, globalCancel context.CancelFunc){
    defer globalCancel()

    configPath := "config.yml"
    config, err := loadConfig(configPath)
    if err != nil {
        log.Printf("Could not load config file: %v", err)
        return
    }

    log.Printf("Loaded '%v'", configPath)

    actions := make(chan ProgramAction, 10)

    currentPlaying := NoMusic
    icon := gtk.NewStatusIconFromFile("off.png")
    icon.Connect("activate", func() {
        menu := makePopup(config, currentPlaying, actions)
        menu.Popup(nil, nil, nil, nil, 0, gtk.GetCurrentEventTime())
    })
    icon.SetTooltipText("Not playing")
    icon.SetVisible(true)

    doPlay := func(name string, url string) (context.Context, context.CancelFunc) {
        quit, cancel := context.WithCancel(globalQuit)
        /* a command context will send SIGKILL if the context is cancelled,
         * so we use a separate context to deal with the process
         */
        killQuit, killCancel := context.WithCancel(context.Background())
        /* FIXME: be able to run with ffmpeg, gstreamer, maybe some other players */
        command := exec.CommandContext(killQuit, "mplayer", "-prefer-ipv4", "-loop", "0", url)
        err := command.Start()

        if err != nil {
            log.Printf("Could not start playing '%v': %v", name, err)
        } else {
            log.Printf("Launched mplayer with pid %v\n", command.Process.Pid)
        }

        go func(){
            <-quit.Done()
            command.Process.Signal(syscall.SIGTERM)
            time.Sleep(2 * time.Second)
            // make sure the process dies
            killCancel()
        }()

        go func(){
            command.Wait()
            log.Printf("Mplayer command stopped %v", command.Process.Pid)
            cancel()
        }()

        return quit, cancel
    }

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
                        icon.SetFromFile("off.png")
                        icon.SetTooltipText("Not playing")
                        playCancel()
                        playQuit, playCancel = context.WithCancel(context.Background())
                    }

                    play, ok := action.(*ProgramActionPlay)
                    if ok {
                        url := config.GetUrl(play.Name)
                        if url != "" {
                            log.Printf("Play url '%v' = '%v'", play.Name, url)
                            /* FIXME: race condition */
                            currentPlaying = play.Name

                            path := "on.png"
                            icon.SetFromFile(path)
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

func fixTty(){
    /* run 'stty sane' */
    /* only need this if we show the output of mplayer */
}

func main(){
    log.SetFlags(log.Lshortfile | log.Ldate | log.Lmicroseconds)
    log.Printf("Initializing")
    gtk.Init(&os.Args)

    globalQuit, globalCancel := context.WithCancel(context.Background())

    signaler := make(chan os.Signal, 10)
    signal.Notify(signaler, syscall.SIGINT, syscall.SIGTERM)

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

    run(globalQuit, globalCancel)

    <-globalQuit.Done()

    fixTty()
    log.Printf("Goodbye")
}
