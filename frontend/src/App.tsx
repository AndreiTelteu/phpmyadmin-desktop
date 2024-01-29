import { Tabs } from "./ui";
import TabServer from './TabServer';
import TabComponents from './TabComponents';

export default function App() {
  return (
    <div data-kb-theme="dark">
      <Tabs
        tabs={[
          { label: 'Servers', content: <TabServer /> },
          { label: 'Components', content: <TabComponents /> },
        ]}
      />
    </div>
  );
};
