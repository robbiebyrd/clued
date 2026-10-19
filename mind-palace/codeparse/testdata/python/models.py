# Copyright 2026 Example. A file header, not a doc comment.
"""Models of the inventory service.

Every model is a plain class or a dataclass.
"""

from __future__ import annotations

import enum
from dataclasses import dataclass, field
from typing import ClassVar, Protocol

# DEFAULT_LIMIT bounds a page of results.
DEFAULT_LIMIT = 50
_MAX_RETRIES: int = 3
registry: dict[str, "Item"] = {}
version_info = (1, 2)
HOST, PORT = "localhost", 8080
_cache = None

type ItemID = int
type Pair[T] = tuple[T, T]


class Color(enum.Enum):
    """A colour an item comes in."""

    RED = 1
    GREEN = 2


class Sized(Protocol):
    """Anything with a size."""

    def size(self) -> int:
        """Returns the size in bytes."""
        ...


@dataclass(frozen=True)
class Item:
    """One item of the inventory.

    Items are immutable.
    """

    kind: ClassVar[str] = "item"
    name: str
    price: float = 0.0
    tags: list[str] = field(default_factory=list)

    def __post_init__(self) -> None:
        if self.price < 0:
            raise ValueError("negative price")

    # total returns the price of n items.
    def total(self, n: int = 1) -> float:
        return self.price * n

    @property
    def label(self) -> str:
        """The item's display label."""
        return f"{self.name} ({self.price})"

    @staticmethod
    def parse(text: str) -> Item:
        name, _, price = text.partition(":")
        return Item(name=name, price=float(price))

    @classmethod
    async def fetch(cls, client, key: str) -> "Item":
        data = await client.get(key)
        return cls(**data)

    def _private(self):
        pass

    class Meta:
        """Options of the model."""

        ordering = ["name"]


def make_item(name: str, *, price: float = 0.0) -> Item:
    """Builds an item with a price."""
    return Item(name=name, price=price)


async def load_all(client) -> list[Item]:
    return [await Item.fetch(client, k) for k in await client.keys()]


def _helper():
    return DEFAULT_LIMIT
