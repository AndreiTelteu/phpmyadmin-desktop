import { For } from 'solid-js';
import useServersStore from './serversStore';
import { NewWindow, ChoosePrivateKey } from '../wailsjs/go/main/App';

export default function TabServer() {
    const [serversStore, serversActions] = useServersStore();
    return (
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
    )
}