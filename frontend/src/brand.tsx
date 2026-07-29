export function AppMark(props: { size?: number }) {
    const size = () => props.size ?? 20;
    return (
        <svg width={size()} height={size()} viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <rect x="1.5" y="4.5" width="21" height="15" rx="2" fill="currentColor" opacity="0.18" />
            <rect x="1.5" y="4.5" width="21" height="15" rx="2" stroke="currentColor" stroke-width="1.4" />
            <path d="M1.9 8h20.2" stroke="currentColor" stroke-width="1.4" />
            <circle cx="4.4" cy="6.3" r="0.75" fill="currentColor" />
            <circle cx="6.8" cy="6.3" r="0.75" fill="currentColor" />
            <path d="M5 13.6c1.6-2.3 2.6-2.1 3.7 0s2.3 2.2 3.8 0 2.6-2 3.7 0" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
            <path d="M18.4 11.2v4.8M16 13.6h4.8" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
        </svg>
    );
}

export function FrankenMark(props: { size?: number }) {
    const size = () => props.size ?? 18;
    return (
        <svg width={size()} height={size()} viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M13.4 2.5 6.1 13.2h4.3L8.9 21.5l8-11.4h-4.4l.9-7.6z" fill="currentColor" />
            <path d="M4.5 5.5l2.2-2.2M5.3 8.2 8.4 5.1M16.5 18.5l2.2-2.2M17.3 21.2l3.1-3.1" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
        </svg>
    );
}

export function SailMark(props: { size?: number }) {
    const size = () => props.size ?? 18;
    return (
        <svg width={size()} height={size()} viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M13.6 2.2c3.1 2.5 4.9 6.7 4.9 12l-6 1.7V2.2h1.1z" fill="currentColor" opacity="0.92" />
            <path d="M10.9 4.4v11.5L4 13.6c1-4 3.2-7.3 6.9-9.2z" fill="currentColor" opacity="0.62" />
            <path d="M4 15.6l7 2.1v1.2l-7-2.2v-1.1z" fill="currentColor" opacity="0.62" />
            <path d="M2.4 19.4c2 1.3 5.9 2.2 9.6 2.2 3.8 0 7.6-.9 9.6-2.2h-2.3c-2 .8-4.7 1.3-7.3 1.3-2.6 0-5.3-.5-7.3-1.3H2.4z" fill="currentColor" />
            <path d="M11.2 2.2h1.1v13.7l6.2-1.7v1.5l-7.3 2v-15.5z" fill="currentColor" opacity="0.01" />
        </svg>
    );
}

export function BrandTile(props: {
    kind: 'app' | 'frankenphp' | 'pma';
}) {
    const mark = () => {
        if (props.kind === 'frankenphp') return <FrankenMark size={17} />;
        if (props.kind === 'pma') return <SailMark size={17} />;
        return <AppMark size={17} />;
    };

    return (
        <span
            class="mark-tile"
            classList={{
                'mark-tile--accent': props.kind === 'app',
                'mark-tile--frankenphp': props.kind === 'frankenphp',
                'mark-tile--pma': props.kind === 'pma',
            }}
            title={props.kind === 'app' ? undefined : 'Brand mark'}
            aria-hidden="true"
        >
            {mark()}
        </span>
    );
}
