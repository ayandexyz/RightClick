import QtQuick
import qs.Commons
import qs.Ui

Panel {
  id: root
  moduleName: "io.github.ayan.file-converter"
  manageIpc: false
  property var anchorItem: null
  property var hostWidget: null
  property var service: null

  function open() { root.controller.show() }
  function close() { root.controller.hide() }
  function toggle() { root.opened ? root.close() : root.open() }
  function cancelJob(id) {
    if (root.hostWidget && typeof root.hostWidget.cancelJob === "function") root.hostWidget.cancelJob(id)
  }

  KeyboardPanel {
    id: panel
    anchorItem: root.anchorItem
    owner: root.hostWidget || root
    bar: root.bar
    open: root.opened
    contentWidth: panel.fittedContentWidth(Style.space(360))
    contentHeight: panel.fittedContentHeight(content.implicitHeight)

    Column {
      id: content
      width: parent.width
      spacing: Style.space(10)
      Text {
        text: "File conversions"
        color: root.barForeground
        font.pixelSize: Style.font.subtitle
        font.bold: true
      }
      Text {
        visible: !root.service || root.service.activeCount === 0
        text: "No active conversions"
        color: root.barForeground
        opacity: 0.65
        font.pixelSize: Style.font.body
      }
      Repeater {
        model: root.service ? Object.keys(root.service.jobs) : []
        delegate: Column {
          width: content.width
          spacing: Style.space(4)
          property var job: root.service.jobs[modelData]
          Row {
            width: parent.width
            spacing: Style.space(8)
            Text {
              width: parent.width - cancelButton.width - parent.spacing
              text: String(job.label || job.preset || modelData)
              color: root.barForeground
              elide: Text.ElideMiddle
              font.pixelSize: Style.font.body
            }
            Text {
              text: Math.round(Number(job.progress || 0) * 100) + "%"
              color: root.barForeground
              font.pixelSize: Style.font.body
            }
            Rectangle {
              id: cancelButton
              width: Style.space(24)
              height: Style.space(24)
              color: cancelTap.pressed
                ? Qt.rgba(root.barForeground.r, root.barForeground.g, root.barForeground.b, 0.22)
                : "transparent"
              radius: height / 2
              Text {
                anchors.centerIn: parent
                text: "×"
                color: root.barForeground
                font.pixelSize: Style.font.body
              }
              TapHandler {
                id: cancelTap
                onTapped: root.cancelJob(modelData)
              }
            }
          }
          Rectangle {
            width: parent.width
            height: Style.space(6)
            radius: height / 2
            color: Qt.rgba(root.barForeground.r, root.barForeground.g, root.barForeground.b, 0.18)
            Rectangle {
              width: parent.width * Math.max(0, Math.min(1, Number(job.progress || 0)))
              height: parent.height
              radius: parent.radius
              color: root.barForeground
            }
          }
        }
      }
    }
  }
}
