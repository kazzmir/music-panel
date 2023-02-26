A GTK panel applet that provides easy access to change the music station being listened to.

Ubuntu deps

sudo apt install libgdk-pixbuf2.0-dev libpango1.0-dev libgtk2.0-dev

Build with 'make'

Usage:

Copy config.yml.sample to config.yml, and edit config.yml with url's of streaming music that mplayer can play. Then run 'music-panel', and an icon should appear in your panel.

Systemd integration:

* Copy etc/music-panel.service to ~/.config/systemd/user
* Copy music-panel binary to /usr/local/bin
* Copy config.yml to ~/.config/music-panel

```
$ systemctl --user enable music-panel
$ systemctl --user start music-panel
```
