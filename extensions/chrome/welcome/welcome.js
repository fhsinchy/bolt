document.getElementById('open-settings').addEventListener('click', () => {
  chrome.runtime.sendMessage({ type: 'open-settings' });
});
