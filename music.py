#!/usr/bin/env python

import pygtk
import gtk
import gobject
pygtk.require('2.0')
gtk.gdk.threads_init()

class Something:
    def __init__(self):
        self.icon = None
        self.update_icon('off')
        self.music = None
        self.playlist = None
        self.icon.connect('activate', self.icon_click)
        with open('config.yml') as file:
            import yaml
            self.config = yaml.load(file.read())
        self.icon.set_visible(True)

    def update_icon(self, kind):
        path = '{}.png'.format(kind)
        if self.icon is not None:
            self.icon.set_from_file(path)
        else:
            self.icon = gtk.status_icon_new_from_file(path)

    def stop_music(self):
        if self.music is not None:
            # print("Killing {}".format(self.music.pid))
            #self.music.kill()
            self.music.send_signal(2)
            self.music.wait()
            self.music = None
        self.update_icon('off')

    def play_music(self, playlist):
        import subprocess
        self.stop_music()
        if playlist in self.config:
            self.music = subprocess.Popen(['mplayer', self.config[playlist]['url']])
            self.icon.set_tooltip("Playing {}".format(self.config[playlist]['name']))
            self.update_icon('on')
            return True
        return False
        # print("Playing {}".format(self.music.pid))

    def quit(self):
        self.stop_music()
        import subprocess
        subprocess.call(['stty', 'sane'])

    def make_popup(self):
        menu = gtk.Menu()
        no_music = 'no-music'
        names = self.config.keys()
        for name in [no_music] + sorted(names):
            real_name = 'None'
            if name in self.config:
                real_name = self.config[name]['name']
            if name == self.playlist:
                item = gtk.CheckMenuItem(real_name)
                item.set_active(True)
            else:
                item = gtk.MenuItem(real_name)
            def make_click(name):
                def click(object):
                    self.playlist = name
                    if name == no_music:
                        self.stop_music()
                    else:
                        self.play_music(name)
                return click
            item.connect('activate', make_click(name))
            item.show()
            menu.append(item)

        menu.popup(None, None, None, 0, gtk.get_current_event_time())

    def icon_click(self, gtk_object):
        """
        Handle a click
        """
        self.make_popup()
        # print(gtk_object)
        # self.playing = not self.playing
        # self.update_icon()
        # print 'quit'
        # gtk.main_quit()

    def update(self):
        pass
        # print("update", self)
        # gobject.timeout_add(100, self.update)

def main():
    thing = Something()
    # gobject.timeout_add(100, thing.update)
    try:
        gtk.main()
    finally:
        thing.quit()

if __name__ == "__main__":
    main()

