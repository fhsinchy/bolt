package notify

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Send displays a desktop notification. It is best-effort and non-blocking.
func Send(title, message string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("notify-send", title, message)
	case "darwin":
		script := fmt.Sprintf(`display notification %q with title %q`, message, title)
		cmd = exec.Command("osascript", "-e", script)
	case "windows":
		ps := fmt.Sprintf(`[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null; $xml = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02); $xml.GetElementsByTagName('text')[0].AppendChild($xml.CreateTextNode('%s')) > $null; $xml.GetElementsByTagName('text')[1].AppendChild($xml.CreateTextNode('%s')) > $null; [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('Bolt').Show([Windows.UI.Notifications.ToastNotification]::new($xml))`, title, message)
		cmd = exec.Command("powershell", "-Command", ps)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}
