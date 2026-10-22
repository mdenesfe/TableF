# dmgbuild settings for the TableF installer window. Positions match
# scripts/dmg/background.py (points, from the top-left of the window).
# Run through scripts/release-mac.sh, which passes:
#   -D app=build/bin/TableF.app -D background=build/dmg/background.tiff
import os.path

application = defines.get('app', 'build/bin/TableF.app')  # noqa: F821 (dmgbuild provides `defines`)
appname = os.path.basename(application)

format = 'ULMO'  # LZMA: the smallest download (macOS 10.15+)
filesystem = 'HFS+'
size = None

files = [application]
symlinks = {'Applications': '/Applications'}
icon_locations = {appname: (180, 215), 'Applications': (480, 215)}

background = defines.get('background', 'build/dmg/background.tiff')  # noqa: F821
window_rect = ((200, 160), (660, 400))
default_view = 'icon-view'
show_status_bar = False
show_tab_view = False
show_toolbar = False
show_pathbar = False
show_sidebar = False
show_icon_preview = False
include_icon_view_settings = True
include_list_view_settings = False

icon_size = 112
text_size = 13
arrange_by = None
grid_offset = (0, 0)
label_pos = 'bottom'
