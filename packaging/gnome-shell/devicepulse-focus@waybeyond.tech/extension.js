// DevicePulse Focus Reporter — GNOME Shell extension.
//
// Owns org.devicepulse.Shell on the user's session bus and answers
// GetFocusedWindow() with the WM class and title of the currently focused
// window. This gives the DevicePulse agent reliable focus data on GNOME
// Wayland, where org.gnome.Shell.Eval is disabled by default since GNOME 41
// (Ubuntu 22.10+/24.04 ship GNOME 43/46).
//
// Written in the GNOME 45+ ESM style (Ubuntu 24.04 = GNOME 46).

import Gio from 'gi://Gio';

const IFACE = `
<node>
  <interface name="org.devicepulse.Shell">
    <method name="GetFocusedWindow">
      <arg type="s" direction="out" name="wm_class" />
      <arg type="s" direction="out" name="title" />
    </method>
  </interface>
</node>
`;

export default class DevicePulseFocusExtension {
    enable() {
        this._impl = Gio.DBusExportedObject.wrapJSObject(IFACE, this);
        this._ownerId = Gio.bus_own_name(
            Gio.BusType.SESSION,
            'org.devicepulse.Shell',
            Gio.BusNameOwnerFlags.NONE,
            (conn) => {
                try {
                    this._impl.export(conn, '/org/devicepulse/Shell');
                } catch (e) {
                    console.error(`DevicePulseFocus: D-Bus export failed: ${e}`);
                }
            },
            null,                       // on_name_acquired — nothing to do
            () => {}                    // on_name_lost — disable() cleans up
        );
    }

    disable() {
        if (this._ownerId !== undefined) {
            Gio.bus_unown_name(this._ownerId);
            this._ownerId = undefined;
        }
        if (this._impl) {
            try {
                this._impl.unexport();
            } catch (e) {
                // already unexported when the bus name was lost
            }
            this._impl = null;
        }
    }

    // D-Bus method: returns [wm_class, title] of the focused window.
    GetFocusedWindow() {
        let win = null;
        try {
            win = global.display.get_focus_window();
        } catch (e) {
            win = null;
        }
        if (!win) {
            return ['', ''];
        }
        let wmClass = '';
        let title = '';
        try { wmClass = win.get_wm_class() ?? ''; } catch (e) { wmClass = ''; }
        try { title = win.get_title() ?? ''; } catch (e) { title = ''; }
        return [wmClass, title];
    }
}
