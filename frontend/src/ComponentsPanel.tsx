import { For, Show, onMount } from 'solid-js';
import { createStore } from 'solid-js/store';
import { CheckLatestVersion } from '../bindings/github.com/andreitelteu/phpmyadmin-desktop/app';
import { BrandTile } from './brand';

export const APP_VERSION = '0.0.3';

type ComponentState = {
    id: 'app' | 'frankenphp' | 'pma';
    name: string;
    description: string;
    status: 'checking' | 'ok' | 'error';
    latest?: string;
    latestLink?: string;
    note?: string;
};

const initialComponents: ComponentState[] = [
    {
        id: 'app',
        name: 'Desktop App',
        description: 'This launcher',
        status: 'ok',
        latest: APP_VERSION,
    },
    {
        id: 'frankenphp',
        name: 'FrankenPHP Runtime',
        description: 'PHP server runtime (includes PHP)',
        status: 'checking',
    },
    {
        id: 'pma',
        name: 'phpMyAdmin',
        description: 'MySQL / MariaDB administration client',
        status: 'checking',
    },
];

export function ComponentsPanel() {
    const [components, setComponents] = createStore<ComponentState[]>([...initialComponents]);

    onMount(async () => {
        for (let i = 0; i < components.length; i++) {
            const item = components[i];
            if (item.id === 'app') continue;
            try {
                const [latest, link] = await CheckLatestVersion(item.id);
                if (latest && link) {
                    setComponents(i, {
                        status: 'ok',
                        latest,
                        latestLink: link,
                    });
                } else {
                    setComponents(i, { status: 'error', note: 'No release found for this platform.' });
                }
            } catch (err) {
                const note = String(err) === 'not-found-release'
                    ? 'No release found for this platform.'
                    : 'Version check failed. Check your network connection.';
                setComponents(i, { status: 'error', note });
            }
        }
    });

    return (
        <section class="panel" aria-label="Runtime components">
            <div class="panel__head">
                <h2 class="panel__title">Runtime components</h2>
            </div>
            <p class="panel__hint">
                Latest upstream releases. Automatic installation is planned, not yet available.
            </p>
            <div class="panel__divider" />
            <div class="panel__body">
                <div class="component-list" role="list">
                    <For each={components}>
                        {(component) => (
                            <div class="component-row" role="listitem">
                                <BrandTile kind={component.id} />
                                <div class="component-row__info">
                                    <div class="component-row__name">
                                        {component.name}
                                        <Show when={component.id === 'app'}>
                                            <span class="component-row__version">v{APP_VERSION}</span>
                                        </Show>
                                    </div>
                                    <div class="component-row__desc">{component.description}</div>
                                </div>
                                <div class="component-row__state">
                                    <Show
                                        when={component.status !== 'checking'}
                                        fallback={
                                            <span class="status-pill">
                                                <span class="status-dot status-dot--pending" />
                                                Checking…
                                            </span>
                                        }
                                    >
                                        <Show
                                            when={component.status === 'ok'}
                                            fallback={
                                                <>
                                                    <span class="status-pill">
                                                        <span class="status-dot status-dot--err" />
                                                        Could not check
                                                    </span>
                                                    <span class="status-note">{component.note}</span>
                                                </>
                                            }
                                        >
                                            <span class="status-pill">
                                                <span class="status-dot status-dot--ok" />
                                                Latest: <span class="mono">{component.latest}</span>
                                            </span>
                                            <Show when={component.latestLink}>
                                                <a
                                                    class="status-link"
                                                    href={component.latestLink}
                                                    target="_blank"
                                                    rel="noreferrer noopener"
                                                >
                                                    {component.latestLink}
                                                </a>
                                            </Show>
                                        </Show>
                                    </Show>
                                </div>
                            </div>
                        )}
                    </For>
                </div>
            </div>
        </section>
    );
}
