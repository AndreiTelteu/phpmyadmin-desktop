import { For, type Component } from 'solid-js';
import useServersStore from './serversStore';
import { NewWindow } from '../wailsjs/go/main/App';

const App: Component = () => {;
  const [serversStore, serversActions] = useServersStore();
  return (
    <div >
      <header >
        <p>
          Edit <code>src/App.tsx</code> and save to reload.
        </p>
        
        <For
          each={serversStore.list}
          fallback={(
            <div>
              <p>no servers</p>
              <button onClick={() => serversActions.newServer()}>add server</button>
            </div>
          )}
          children={(server) => (
            <div>
              <p>server: {JSON.stringify(server)}</p>
              <button onClick={() => NewWindow(server.id)}>open</button>
            </div>
          )}
        />
        
      </header>
    </div>
  );
};

export default App;
