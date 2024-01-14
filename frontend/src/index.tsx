/* @refresh reload */
import { render } from 'solid-js/web';

import './index.css';
import App from './App';
import { GetServerID } from '../wailsjs/go/main/App';
import PMA from './PMA';

GetServerID().then(res => {
    const root = document.getElementById('root') as HTMLElement;
    const app = res == 'nil' ? () => <App /> : () => <PMA serverId={res} />
    root.innerHTML = '';
    render(app, root);
})
.catch(err => {
    console.log('Error GetServerID',err);
});
