import { For, type Component } from 'solid-js';
import useServersStore from './serversStore';
import { NewWindow, ChoosePrivateKey } from '../wailsjs/go/main/App';
import { Tabs } from "./ui";

const App: Component = () => {;
  const [serversStore, serversActions] = useServersStore();
  return (
    <div data-kb-theme="dark">
      <Tabs
        tabs={[
          { label: 'Servers', content: <div>profile details</div> },
          { label: 'Components', content: <div>dashboard details</div> },
        ]}
      />
      <header >
        <For
          each={serversStore.list}
          fallback={(
            <div>
              <p>no servers</p>
              <button onClick={() => serversActions.newServer()}>add server</button>
            </div>
          )}
          children={(server, index) => (
            <div>
              <p>server: {JSON.stringify(server)}</p>
              <input value={server.host} onInput={(e) => serversActions.updateServer(index(), { host: e.currentTarget.value })} />
              <button onClick={() => NewWindow(server.id)}>open</button>
              <button onClick={() => {
                ChoosePrivateKey().then((file) => {
                  if (!file) return;
                  serversActions.set('list', index(), 'tunnel', 'privateKey', file);
                  console.log({file});
                })
                .catch(err => {
                  console.log("ERROR choosing file",err);
                });
              }}>choose file</button>
            </div>
          )}
        />
        
      </header>
    </div>
  );
};

export default App;
