import { Show, createSignal, onMount } from 'solid-js';
import useServersStore from './serversStore';
import { AppMark } from './brand';

export default function PMA(params: {
    serverId: string,
}) {
    const [serversStore] = useServersStore();
    const [ready, setReady] = createSignal(false);

    onMount(() => {
        setTimeout(() => setReady(true), 300);
    });

    const server = () => serversStore.list.find((s) => s.id === params.serverId);

    return (
        <div class="shell">
            <header class="titlebar">
                <div class="titlebar__brand">
                    <AppMark size={20} />
                    <span class="titlebar__name">
                        {server()?.name || 'phpMyAdmin session'}
                        <small>{server()?.host ? `${server()!.host}:${server()!.port || 3306}` : params.serverId}</small>
                    </span>
                </div>
            </header>
            <main class="body">
                <div class="stack">
                    <section class="panel" aria-label="Session status">
                        <div class="panel__head">
                            <h2 class="panel__title">Session</h2>
                        </div>
                        <div class="panel__divider" />
                        <div class="empty">
                            <div class="empty__mark"><AppMark size={34} /></div>
                            <h3 class="empty__title">phpMyAdmin runtime not available yet</h3>
                            <p class="empty__desc">
                                This window is reserved for the dedicated phpMyAdmin session of this connection.
                                Serving phpMyAdmin through the local PHP runtime is planned and not implemented yet.
                            </p>
                            <Show when={ready()}>
                                <p class="empty__desc mono" style={{ 'margin-top': '6px' }}>
                                    connection id: {params.serverId}
                                </p>
                            </Show>
                        </div>
                    </section>
                </div>
            </main>
        </div>
    );
}
