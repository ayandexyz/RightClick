import QtQuick
import qs.Ui

BarWidget {
  id: root
  moduleName: "io.github.ayan.file-converter"
  property var shell: null
  property var service: null
  readonly property int activeCount: service ? service.activeCount : 0
  readonly property real overallProgress: service ? service.overallProgress : 0

  function findService() {
    var found = null
    if (root.bar && root.bar.shell && typeof root.bar.shell.serviceFor === "function")
      found = root.bar.shell.serviceFor(root.moduleName)
    else if (root.shell && typeof root.shell.serviceFor === "function")
      found = root.shell.serviceFor(root.moduleName)
    if (found && found !== root.service) {
      root.service = found
      root.injectPanel()
    }
  }

  function cancelJob(id) { if (root.service) root.service.cancelJob(id) }

  readonly property bool opened: panelLoader.item ? panelLoader.item.opened === true : false
  readonly property bool popoutSwitchClosing: false
  implicitWidth: 52
  implicitHeight: button.implicitHeight

  function open() { if (panelLoader.item) panelLoader.item.open() }
  function close() { if (panelLoader.item) panelLoader.item.close() }
  function toggle() { if (panelLoader.item) panelLoader.item.toggle() }
  function closeForPopoutSwitch() { close() }
  function injectPanel() {
    if (!panelLoader.item) return
    panelLoader.item.bar = root.bar
    panelLoader.item.anchorItem = button
    panelLoader.item.hostWidget = root
    panelLoader.item.service = root.service
  }

  Loader {
    id: panelLoader
    active: true
    source: Qt.resolvedUrl("Panel.qml")
    visible: false
    onLoaded: { root.injectPanel(); Qt.callLater(root.injectPanel) }
  }

  Timer {
    interval: 1000
    repeat: true
    running: root.service === null
    triggeredOnStart: true
    onTriggered: root.findService()
  }

  WidgetButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: !root.service || root.activeCount === 0 ? "⇣"
      : root.service.runningCount === 0 ? "✓"
      : "⇣ " + root.service.filesDone + "/" + root.service.filesTotal
    tooltipText: root.service && root.service.fileSummary !== "" ? "File conversions: " + root.service.fileSummary : "File conversion progress"
    onPressed: function(buttonCode) {
      if (buttonCode === Qt.LeftButton) root.toggle()
    }
  }
}
