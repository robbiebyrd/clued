import React, { useState, useEffect as useMountEffect } from 'react';
import PropTypes from 'prop-types';
import * as UI from './ui';
import Button from './Button.jsx';
import './App.css';

/** Greeting renders a name in a heading. */
function Greeting({ name }) {
  return <h1 className="greeting">Hello, {name}!</h1>;
}

Greeting.propTypes = { name: PropTypes.string.isRequired };

/** TodoList shows the items and a button that adds one. */
export const TodoList = ({ items, onAdd }) => (
  <UI.Panel title="Todo">
    <ul>
      {items.map((item) => (
        <TodoItem key={item.id} {...item} />
      ))}
    </ul>
    <Button onClick={() => onAdd(prompt('What?'))}>Add</Button>
  </UI.Panel>
);

const TodoItem = React.memo(function TodoItem({ title, done }) {
  return <li className={done ? 'done' : ''}>{title}</li>;
});

export default function App() {
  const [items, setItems] = useState([]);
  useMountEffect(() => {
    loadItems().then(setItems);
  }, []);
  const add = (title) => setItems([...items, { id: items.length, title }]);
  return (
    <React.Fragment>
      <Greeting name="you" />
      <TodoList items={items} onAdd={add} />
    </React.Fragment>
  );
}

class ErrorBoundary extends React.Component {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    return this.state.failed ? <p>Something broke.</p> : this.props.children;
  }
}

async function loadItems() {
  const { fetchItems } = await import('./api.js');
  return fetchItems();
}

export { ErrorBoundary };
