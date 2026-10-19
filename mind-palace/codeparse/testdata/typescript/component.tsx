// The tsx dialect: components, hooks, generics in arrow functions and JSX
// references to other components.
import React, { useState, type ReactNode } from "react";
import * as UI from "./ui";
import { Icon } from "./icon";

export interface ButtonProps {
  label: string;
  onClick(): void;
  children?: ReactNode;
}

/** A button with an icon and a click counter. */
export function Button({ label, onClick, children }: ButtonProps): JSX.Element {
  const [count, setCount] = useState<number>(0);
  return (
    <div className="button">
      <Icon name="star" />
      <UI.Label text={label} />
      <button
        onClick={() => {
          setCount(count + 1);
          onClick();
        }}
      >
        {children}
      </button>
    </div>
  );
}

export const Card: React.FC<{ title: string }> = ({ title }) => (
  <section>
    <UI.Heading level={2}>{title}</UI.Heading>
  </section>
);

const identity = <T,>(value: T): T => value;

export default class Panel extends React.Component<ButtonProps> {
  render() {
    return <Button {...this.props} />;
  }
}
