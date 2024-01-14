import type { Component } from 'solid-js';

import { NewWindow } from '../wailsjs/go/main/App';
import * as ConfigStore from '../wailsjs/go/wailsconfigstore/ConfigStore';

const App: Component = () => {;
  return (
    <div >
      <header >
        <p>
          Edit <code>src/App.tsx</code> and save to reload.
        </p>
        <button
          onClick={() => {
            // NewWindow('dada');
            ConfigStore.Get('servers.json', 'null').then(res => {
              console.log(res);
            })
            // ConfigStore.Set('servers.json', '[{}]').then(res => {
            //   console.log(res);
            // })
          }}
        >
          open pma
        </button>
      </header>
    </div>
  );
};

export default App;
