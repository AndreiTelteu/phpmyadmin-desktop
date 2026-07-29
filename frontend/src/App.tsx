import { createSignal } from 'solid-js';
import { ConnectionsPanel } from './ConnectionsPanel';
import { ComponentsPanel, APP_VERSION } from './ComponentsPanel';
import { SettingsFlyout } from './settings';
import { useTheme } from './theme';
import { AppMark } from './brand';
import { SunIcon, MoonIcon, SettingsIcon } from './icons';

export default function App() {
    const theme = useTheme();
    const [settingsOpen, setSettingsOpen] = createSignal(false);

    return (
        <div class="shell">
            <header class="titlebar">
                <div class="titlebar__brand">
                    <AppMark size={20} />
                    <span class="titlebar__name">
                        phpMyAdmin Desktop
                        <small>Connection manager</small>
                    </span>
                </div>
                <div class="titlebar__spacer" />
                <div class="titlebar__actions">
                    <button
                        type="button"
                        class="iconbtn"
                        aria-label={theme.resolved() === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
                        title={theme.resolved() === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
                        onClick={() => theme.toggle()}
                    >
                        {theme.resolved() === 'dark' ? <SunIcon /> : <MoonIcon />}
                    </button>
                    <button
                        type="button"
                        class="iconbtn"
                        aria-label="Settings"
                        aria-haspopup="dialog"
                        aria-expanded={settingsOpen()}
                        title="Settings"
                        onClick={() => setSettingsOpen((v) => !v)}
                    >
                        <SettingsIcon />
                    </button>
                </div>
            </header>

            <main class="body">
                <div class="stack">
                    <ConnectionsPanel />
                    <ComponentsPanel />
                </div>
            </main>

            <SettingsFlyout
                open={settingsOpen()}
                onClose={() => setSettingsOpen(false)}
                theme={theme}
                appVersion={APP_VERSION}
            />
        </div>
    );
}
