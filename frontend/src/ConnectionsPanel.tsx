import { Accessor, For, Show, createSignal } from 'solid-js';
import useServersStore from './serversStore';
import { NewWindow, ChoosePrivateKey } from '../bindings/github.com/andreitelteu/phpmyadmin-desktop/app';
import { ChevronIcon, AddIcon, OpenIcon, KeyIcon, TunnelIcon, DatabaseIcon, CloseIcon } from './icons';
import { Toggle } from './settings';

export function ConnectionsPanel() {
    const [serversStore, serversActions] = useServersStore();
    const [expandedIndex, setExpandedIndex] = createSignal<number | null>(null);
    const [openError, setOpenError] = createSignal('');

    function addConnection() {
        serversActions.newServer();
        const index = serversStore.list.length - 1;
        setExpandedIndex(index >= 0 ? index : null);
    }

    function toggleRow(index: number) {
        setExpandedIndex(expandedIndex() === index ? null : index);
    }

    function open(server: { id: string; name: string }) {
        setOpenError('');
        NewWindow(server.id).catch((err) => {
            setOpenError(`Could not open ${server.name || 'this connection'}: ${err}`);
        });
    }

    return (
        <section class="panel" aria-label="Connections">
            <div class="panel__head">
                <h2 class="panel__title">Connections</h2>
                <div class="panel__head-actions">
                    <button type="button" class="btn btn--sm" onClick={addConnection}>
                        <AddIcon /> Add connection
                    </button>
                </div>
            </div>
            <p class="panel__hint">
                Saved database environments. Select a row to edit it, double-click to open a session.
            </p>
            <div class="panel__divider" />
            <div class="panel__body">
                <Show
                    when={serversStore.list.length > 0}
                    fallback={
                        <div class="empty">
                            <div class="empty__mark"><DatabaseIcon /></div>
                            <h3 class="empty__title">No connections yet</h3>
                            <p class="empty__desc">Add a MySQL or MariaDB server to launch a dedicated phpMyAdmin session for it. Credentials stay on this machine.</p>
                            <button type="button" class="btn btn--accent" onClick={addConnection}>
                                <AddIcon /> Add your first connection
                            </button>
                        </div>
                    }
                >
                    <div class="conn-list" role="list">
                        <For each={serversStore.list}>
                            {(server, index: Accessor<number>) => {
                                const expanded = () => expandedIndex() === index();
                                return (
                                    <div class="conn-row" role="listitem" aria-expanded={expanded()}>
                                        <div
                                            role="button"
                                            tabindex="0"
                                            class="conn-row__summary"
                                            aria-expanded={expanded()}
                                            aria-label={`Edit ${server.name || 'unnamed connection'}`}
                                            onClick={() => toggleRow(index())}
                                            onKeyDown={(e) => {
                                                if (e.key === 'Enter' || e.key === ' ') {
                                                    e.preventDefault();
                                                    toggleRow(index());
                                                }
                                            }}
                                            onDblClick={() => open(server)}
                                        >
                                            <span class="conn-row__chevron"><ChevronIcon /></span>
                                            <span class="conn-row__main">
                                                <span class="conn-row__name" classList={{ 'conn-row__name--empty': !server.name }}>
                                                    {server.name || 'Unnamed connection'}
                                                </span>
                                                <span class="conn-row__target">
                                                    {server.host ? `${server.username ? server.username + '@' : ''}${server.host}:${server.port || 3306}` : 'no host configured'}
                                                </span>
                                            </span>
                                            <span class="conn-row__tags">
                                                <Show when={server.tunnel?.enabled}>
                                                    <span class="tag tag--tunnel"><TunnelIcon />SSH</span>
                                                </Show>
                                            </span>
                                            <span class="conn-row__actions" onClick={(e) => e.stopPropagation()} onDblClick={(e) => e.stopPropagation()}>
                                                <button type="button" class="btn btn--sm btn--accent" onClick={() => open(server)}>
                                                    <OpenIcon /> Open
                                                </button>
                                            </span>
                                        </div>
                                        <Show when={expanded()}>
                                            <ConnectionEditor
                                                index={index()}
                                                collapse={() => setExpandedIndex(null)}
                                                remove={() => {
                                                    serversActions.removeServer(index());
                                                    setExpandedIndex(null);
                                                }}
                                            />
                                        </Show>
                                    </div>
                                );
                            }}
                        </For>
                    </div>
                </Show>
                <Show when={openError()}>
                    <p class="panel__hint" role="alert" style={{ color: 'var(--danger)', padding: '8px 8px 4px' }}>{openError()}</p>
                </Show>
            </div>
        </section>
    );
}

function ConnectionEditor(props: { index: number; collapse: () => void; remove: () => void }) {
    const [serversStore, serversActions] = useServersStore();
    const server = () => serversStore.list[props.index];
    const [keyError, setKeyError] = createSignal('');

    function update(data: Record<string, any>) {
        serversActions.updateServer(props.index, data);
    }

    function pickKey() {
        setKeyError('');
        ChoosePrivateKey()
            .then((file) => {
                if (!file) return;
                serversActions.set('list', props.index, 'tunnel', 'privateKey', file);
            })
            .catch((err) => {
                setKeyError(`Could not open the file picker: ${err}`);
            });
    }

    return (
        <div class="conn-row__editor">
            <div class="editor-section">
                <span class="editor-section__label">Connection</span>
                <div class="editor-grid">
                    <label class="field field--span">
                        <span>Name</span>
                        <input
                            type="text"
                            placeholder="Production database"
                            value={server()?.name ?? ''}
                            onInput={(e) => update({ name: e.currentTarget.value })}
                        />
                    </label>
                    <label class="field">
                        <span>Host</span>
                        <input
                            type="text"
                            placeholder="db.example.com"
                            value={server()?.host ?? ''}
                            onInput={(e) => update({ host: e.currentTarget.value })}
                        />
                    </label>
                    <label class="field">
                        <span>Port</span>
                        <input
                            type="number"
                            min="1"
                            max="65535"
                            value={server()?.port ?? 3306}
                            onInput={(e) => update({ port: parseInt(e.currentTarget.value, 10) || 0 })}
                        />
                    </label>
                    <label class="field">
                        <span>Username</span>
                        <input
                            type="text"
                            autocomplete="off"
                            value={server()?.username ?? ''}
                            onInput={(e) => update({ username: e.currentTarget.value })}
                        />
                    </label>
                    <label class="field">
                        <span>Password</span>
                        <input
                            type="password"
                            autocomplete="off"
                            placeholder="stored locally"
                            value={server()?.password ?? ''}
                            onInput={(e) => update({ password: e.currentTarget.value })}
                        />
                    </label>
                </div>
            </div>

            <div class="editor-section">
                <span class="editor-section__label">SSH tunnel</span>
                <Toggle
                    checked={!!server()?.tunnel?.enabled}
                    onChange={(checked) => update({ tunnel: { enabled: checked } })}
                    label="Route through an SSH bastion"
                />
                <Show when={server()?.tunnel?.enabled}>
                    <div class="editor-grid editor-grid--tunnel">
                        <label class="field">
                            <span>Bastion host</span>
                            <input
                                type="text"
                                placeholder="bastion.example.com"
                                value={server()?.tunnel?.host ?? ''}
                                onInput={(e) => update({ tunnel: { host: e.currentTarget.value } })}
                            />
                        </label>
                        <label class="field">
                            <span>Bastion port</span>
                            <input
                                type="number"
                                min="1"
                                max="65535"
                                value={server()?.tunnel?.port ?? 22}
                                onInput={(e) => update({ tunnel: { port: parseInt(e.currentTarget.value, 10) || 0 } })}
                            />
                        </label>
                        <label class="field">
                            <span>SSH username</span>
                            <input
                                type="text"
                                autocomplete="off"
                                value={server()?.tunnel?.username ?? ''}
                                onInput={(e) => update({ tunnel: { username: e.currentTarget.value } })}
                            />
                        </label>
                        <label class="field">
                            <span>SSH password</span>
                            <input
                                type="password"
                                autocomplete="off"
                                value={server()?.tunnel?.password ?? ''}
                                onInput={(e) => update({ tunnel: { password: e.currentTarget.value } })}
                            />
                        </label>
                    </div>
                    <div class="keyfile-row">
                        <button type="button" class="btn btn--sm" onClick={pickKey}>
                            <KeyIcon /> Choose private key…
                        </button>
                        <span class="keyfile-path" classList={{ 'keyfile-path--empty': !server()?.tunnel?.privateKey }} title={server()?.tunnel?.privateKey ?? ''}>
                            {server()?.tunnel?.privateKey || 'No key selected'}
                        </span>
                    </div>
                    <Show when={keyError()}>
                        <p class="panel__hint" role="alert" style={{ color: 'var(--danger)', padding: '0' }}>{keyError()}</p>
                    </Show>
                </Show>
            </div>

            <div class="editor-foot">
                <button type="button" class="btn btn--sm btn--ghost" onClick={props.collapse}>Done</button>
                <button
                    type="button"
                    class="btn btn--sm btn--ghost btn--danger-text"
                    onClick={props.remove}
                    aria-label={`Delete ${server()?.name || 'this connection'}`}
                >
                    <CloseIcon /> Delete
                </button>
            </div>
        </div>
    );
}
