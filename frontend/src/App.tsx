import type { Component } from 'solid-js';

import { NewWindow } from '../wailsjs/go/main/App';

const App: Component = () => {;
  return (
    <div >
      <header >
        <p>
          Edit <code>src/App.tsx</code> and save to reload.
        </p>
        <button
          onClick={() => {
            NewWindow('dada');
          }}
        >
          open pma
        </button>
      </header>
    </div>
  );
};

export default App;
