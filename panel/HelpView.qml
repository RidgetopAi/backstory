import QtQuick
import Quickshell
import Quickshell.Io
import qs.Ui
import qs.Commons
import "js/launchers.js" as Launchers
import "js/glyphs.js" as Glyphs

// Help pane (the "?" key): the agent's tools, how to view memories, how to
// use Backstory, and groups. A CLI CLIENT like the rest of the panel: the
// tool list is whatever `backstory tools --json` prints (the Go ToolsV0()
// the MCP server itself serves), so no tool name or description is written
// in this plugin. Swapped in for the This Week body by Panel.qml, the same
// pattern as GroupEditor and MemoryView; refetched whenever it is shown.
Item {
  id: root

  signal closeRequested()

  property var tools: []
  property string errorText: ""

  implicitHeight: content.implicitHeight

  function refresh() {
    toolsProcess.running = false
    toolsProcess.running = true
  }

  onVisibleChanged: {
    if (root.visible) root.refresh()
  }

  Process {
    id: toolsProcess
    command: Launchers.toolsCommand()
    stdout: StdioCollector {
      onStreamFinished: {
        try {
          var parsed = JSON.parse(text || "{}")
          root.tools = parsed.tools || []
          root.errorText = ""
        } catch (e) {
          root.tools = []
          root.errorText = "Could not read the tool list from `backstory tools --json`."
        }
      }
    }
  }

  component HelpHeading: PanelSectionHeader {
    foreground: Color.foreground
    width: content.width
  }

  component HelpText: Text {
    width: content.width
    textFormat: Text.PlainText
    font.family: Style.font.family
    font.pixelSize: Style.font.bodySmall
    color: Color.foreground
    wrapMode: Text.WordWrap
  }

  Column {
    id: content
    width: parent.width
    spacing: Style.spacing.panelGap

    Row {
      width: parent.width

      Text {
        textFormat: Text.PlainText
        text: "Help"
        font.family: Style.font.family
        font.pixelSize: Style.font.title
        font.bold: true
        color: Color.foreground
        width: parent.width - Style.space(24)
      }
      PanelActionButton {
        iconText: Glyphs.back()
        tooltipText: "Back"
        foreground: Color.foreground
        hoverColor: Color.accent
        onClicked: root.closeRequested()
      }
    }

    HelpHeading { text: "AGENT TOOLS" }

    HelpText {
      visible: root.errorText !== ""
      text: root.errorText
    }

    Repeater {
      model: root.tools

      delegate: Column {
        required property var modelData
        width: content.width
        spacing: Style.spacing.labelGap / 2

        Text {
          textFormat: Text.PlainText
          text: modelData.name
          font.family: Style.font.family
          font.pixelSize: Style.font.body
          font.bold: true
          color: Color.foreground
        }
        HelpText { text: modelData.description }
      }
    }

    HelpHeading { text: "MEMORY NAVIGATION" }
    HelpText {
      text: "Select a row with j/k, then press m or click its Memory button. Click a record to expand "
        + "it; Edit, Forget and the history toggle act on it."
    }

    HelpHeading { text: "HOW TO USE" }
    HelpText {
      text: "The top card is the project you are in. Each row's chips are the agents used there, "
        + "newest first. Continue resumes the newest one; \u2192 (or a click on the chips) expands one "
        + "sub-row per agent, \u2190 folds them, and Enter on a sub-row opens that agent in the folder. "
        + "Enter on a row continues it, t opens a terminal there, r refreshes."
    }

    HelpHeading { text: "GROUPS" }
    HelpText {
      text: "A group folds related projects into one RECENT row; you set it, one per project. Click "
        + "the gear for the group editor, or run `backstory group set <group> <project>`."
    }

  }
}
