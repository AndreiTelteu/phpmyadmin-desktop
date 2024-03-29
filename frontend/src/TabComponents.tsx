import { For, Show, createSignal, onMount } from "solid-js";
import { createStore, produce } from "solid-js/store";
import { CheckLatestVersion } from '../wailsjs/go/main/App';
import wailsConfig from '../../wails.json';
import { stylesheet, Button, Progress } from "./ui";

const componentsList: App.ComponentItem[] = [
  { 
    id: 'app', name: 'Desktop App', description: 'The desktop app', version: wailsConfig.info.productVersion,
    img: 'data:image/png;base64,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',
  },
  {
    id: 'php', name: 'PHP Runtime', description: 'PHP server',
    img: 'data:image/png;base64,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',
  },
  {
    id: 'pma', name: 'phpMyAdmin', description: 'MySQL client',
    img: 'data:image/png;base64,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',
  },
];

export default function TabComponents() {
  const [components, setComponents] = createStore([...componentsList]);
  const [installState, setInstallState] = createStore({});
  
  onMount(async () => {
    for (const key in components) {
      const item: App.ComponentItem = {...components[+key]};
      try {
        const [latest, link] = await CheckLatestVersion(item.id);
        if (latest && link) {
          setComponents(+key, {
            latest: latest,
            latest_link: link,
          })
        }
      } catch (err) {
        let latest = 'Error: '+err;
        if (err == 'not-found-release') latest = "Release not found for your OS";
        setComponents(+key, 'latest', latest);
        console.log("ERROR checking latest version",{ c: item, err });
      }
    }
  });
  
  function installComponent(component: App.ComponentItem) {
    setInstallState((s) => ({...s, [component.id]: [0, 'Starting...'] }));
    
  }
  
  return (
    <For
      each={components}
      children={(component) => (
        <div class={styles.compItem}>
          <img src={component.img} class={styles.compImg} />
          <div class={styles.compInfo}>
            <div class={styles.compTitle}>
              {component.name}
              {component.version ? (
                <small> v{component.version}</small>
              ) : ''}
            </div>
            <Show
              when={installState[component.id]}
              children={(
                <Progress
                  value={installState[component.id][0]} maxValue={100}
                  message={installState[component.id][1]}
                />
              )}
              fallback={(
                <div>{component.description}</div>
              )}
            />
          </div>
          <div class={styles.compActions}>
            <Show
              when={installState[component.id]}
              children={(
                <Button disabled>Installing...</Button>
              )}
              fallback={(
                <Button onClick={() => installComponent(component)}>Install</Button>
              )}
            />
          </div>
        </div>
      )}
    />
  )
}

const styles = stylesheet({
  compItem: {
    display: 'flex',
    flexDirection: 'row',
    alignItems: 'center',
    marginBottom: '10px',
    color: 'white',
  },
  compImg: {
    width: '60px',
    height: '60px',
    objectFit: 'contain',
  },
  compInfo: {
    flex: 1,
    paddingLeft: '10px',
  },
  compTitle: {
    fontSize: '16px',
    fontWeight: 'bold',
    'small': {
      fontSize: '12px',
      fontWeight: 'normal',
    },
  },
  compActions: {
    flexShrink: 0,
  },
})
