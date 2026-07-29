import { For, Show, createSignal, onCleanup, onMount } from 'solid-js';
import useServersStore from './serversStore';
import { AppMark } from './brand';
import { SessionStart, SessionStatus, SessionStop } from '../bindings/github.com/andreitelteu/phpmyadmin-desktop/app';

type Phase = 'idle' | 'installing' | 'tunnel' | 'starting' | 'ready' | 'failed';

type ComponentProgressInfo = {
    name: string;
    state: 'pending' | 'downloading' | 'installing' | 'done';
    bytes: number;
    // < 0 means the server gave no Content-Length: render indeterminate.
    total: number;
    version?: string;
};

type SessionProgress = {
    components: ComponentProgressInfo[];
    aggregateBytes: number;
    aggregateTotal: number;
    // false means the aggregate is a bounded estimate, not a measured total.
    aggregateKnown: boolean;
    percent: number;
};

const PHASE_LABELS: Record<string, string> = {
    idle: 'Initializing…',
    installing: 'Installing runtime',
    tunnel: 'Opening SSH tunnel',
    starting: 'Starting phpMyAdmin',
    ready: 'Ready',
    failed: 'Failed',
};

const COMPONENT_LABELS: Record<string, string> = {
    frankenphp: 'FrankenPHP runtime',
    phpmyadmin: 'phpMyAdmin',
    'pma-theme-darkwolf': 'Darkwolf theme',
};

function formatBytes(n: number): string {
    if (!Number.isFinite(n) || n < 0) return '';
    const units = ['B', 'KiB', 'MiB', 'GiB'];
    let value = n;
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) {
        value /= 1024;
        unit += 1;
    }
    return `${value >= 100 || unit === 0 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`;
}

function componentLabel(name: string): string {
    return COMPONENT_LABELS[name] ?? name;
}

export default function PMA(params: {
    serverId: string,
}) {
    const [serversStore] = useServersStore();
    const [phase, setPhase] = createSignal<Phase>('idle');
    const [message, setMessage] = createSignal('');
    const [error, setError] = createSignal('');
    const [progress, setProgress] = createSignal<SessionProgress | null>(null);
    const [attempt, setAttempt] = createSignal(0);

    const server = () => serversStore.list.find((s) => s.id === params.serverId);
    const busy = () => phase() === 'idle' || phase() === 'installing' || phase() === 'tunnel' || phase() === 'starting';
    const downloading = () => (progress()?.components ?? []).some((c) => c.state === 'downloading');

    if (server()?.name) {
        document.title = `PMA ${server()!.name}`;
    }

    let pollTimer: number | undefined;

    async function pollStatus() {
        try {
            const status = JSON.parse(await SessionStatus());
            if (status.phase) setPhase(status.phase as Phase);
            if (status.message) setMessage(status.message);
            if (status.progress) setProgress(status.progress as SessionProgress);
        } catch {
            // status polling is best-effort
        }
    }

    async function launch() {
        setError('');
        setPhase('installing');
        setMessage('Preparing the local phpMyAdmin runtime…');
        // Drop any previous attempt's byte snapshot so a retry does not
        // briefly render the stale totals of a failed session.
        setProgress(null);
        clearInterval(pollTimer);
        pollTimer = window.setInterval(pollStatus, 500);
        try {
            const url = await SessionStart();
            clearInterval(pollTimer);
            window.location.assign(url);
        } catch (err) {
            clearInterval(pollTimer);
            setPhase('failed');
            setError(String(err));
        }
    }

    function retry() {
        setAttempt((n) => n + 1);
        launch();
    }

    onMount(() => {
        launch();
    });

    onCleanup(() => {
        clearInterval(pollTimer);
        // Ensure the runtime child process and tunnel cannot outlive the window.
        SessionStop();
    });

    const statusDetail = () => {
        if (phase() === 'failed') return error();
        return message() || PHASE_LABELS[phase()];
    };

    return (
        <div class="shell">
            <header class="titlebar">
                <div class="titlebar__brand">
                    <AppMark size={20} />
                    <span class="titlebar__name">
                        {server()?.name ? `PMA ${server()!.name}` : 'phpMyAdmin session'}
                        <small>{server()?.host ? `${server()!.host}:${server()!.port || 3306}` : params.serverId}</small>
                    </span>
                </div>
            </header>
            <main class="body">
                <div class="stack">
                    <section class="panel" aria-label="Session status" aria-live="polite">
                        <div class="panel__head">
                            <h2 class="panel__title">{PHASE_LABELS[phase()] ?? 'Session'}</h2>
                            <Show when={busy()}>
                                <span class="conn-row__spinner" aria-hidden="true" />
                            </Show>
                        </div>
                        <div class="panel__divider" />
                        <div class="empty">
                            <div class="empty__mark"><AppMark size={34} /></div>
                            <Show
                                when={phase() !== 'failed'}
                                fallback={
                                    <>
                                        <h3 class="empty__title">Could not start the phpMyAdmin session</h3>
                                        <p class="empty__desc" role="alert" style={{ 'white-space': 'pre-wrap' }}>{statusDetail()}</p>
                                        <button type="button" class="btn btn--accent" onClick={retry}>
                                            Retry
                                        </button>
                                    </>
                                }
                            >
                                <h3 class="empty__title">{PHASE_LABELS[phase()] ?? 'Working…'}</h3>
                                <p class="empty__desc" style={{ 'white-space': 'pre-wrap' }}>{statusDetail()}</p>
                                <Show when={progress() && (phase() === 'installing' || downloading())}>
                                    {(p) => (
                                        <div class="dl-progress" role="status" aria-label="Download progress">
                                            <div class="dl-progress__aggregate">
                                                <div
                                                    class="dl-progress__bar"
                                                    classList={{ 'dl-progress__bar--indeterminate': !p().aggregateKnown }}
                                                    role="progressbar"
                                                    aria-valuemin={0}
                                                    aria-valuemax={100}
                                                    aria-valuenow={p().aggregateKnown ? p().percent : undefined}
                                                >
                                                    <span
                                                        class="dl-progress__fill"
                                                        style={{ width: `${Math.max(2, Math.min(100, p().percent))}%` }}
                                                    />
                                                </div>
                                                <span class="dl-progress__numbers mono">
                                                    {p().aggregateKnown
                                                        ? `${p().percent}% · ${formatBytes(p().aggregateBytes)} / ${formatBytes(p().aggregateTotal)}`
                                                        : `${formatBytes(p().aggregateBytes)} received · ~${p().percent}% estimated`}
                                                </span>
                                            </div>
                                            <ul class="dl-progress__list">
                                                <For each={p().components}>
                                                    {(c) => (
                                                        <li class="dl-progress__row" data-state={c.state}>
                                                            <span class="dl-progress__name">{componentLabel(c.name)}</span>
                                                            <span class="dl-progress__value mono">
                                                                {c.state === 'done' && (c.total > 0 ? `done · ${formatBytes(c.total)}` : 'done')}
                                                                {c.state === 'downloading' && c.total > 0 && `${formatBytes(c.bytes)} / ${formatBytes(c.total)} (${Math.min(99, Math.round((c.bytes / c.total) * 100))}%)`}
                                                                {c.state === 'downloading' && c.total <= 0 && `${formatBytes(c.bytes)} received`}
                                                                {c.state === 'installing' && 'installing…'}
                                                                {c.state === 'pending' && 'waiting…'}
                                                            </span>
                                                        </li>
                                                    )}
                                                </For>
                                            </ul>
                                        </div>
                                    )}
                                </Show>
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
