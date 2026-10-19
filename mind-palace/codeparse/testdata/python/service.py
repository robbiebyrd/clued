import logging
from abc import ABC, abstractmethod
from typing import Generic, Optional, TypeVar

import requests

from .models import Item, Sized
from . import store

log = logging.getLogger(__name__)
T = TypeVar("T")


class ServiceError(Exception):
    """Raised when a call to the service fails."""


class Repository(ABC, Generic[T]):
    @abstractmethod
    def get(self, key: str) -> Optional[T]: ...

    @abstractmethod
    def put(self, key: str, value: T) -> None: ...


class ItemRepository(Repository[Item], store.Base, metaclass=store.Registry):
    def __init__(self, session: requests.Session, base_url: str) -> None:
        super().__init__()
        self.session = session
        self.base_url = base_url

    def get(self, key: str) -> Optional[Item]:
        try:
            resp = self.session.get(f"{self.base_url}/{key}")
            resp.raise_for_status()
        except (requests.HTTPError, ConnectionError) as exc:
            log.warning("get %s: %s", key, exc)
            raise ServiceError(key) from exc
        except store.NotFound:
            return None
        return Item.parse(resp.text)

    def put(self, key: str, value: Item) -> None:
        payload: dict[str, store.Value] = encode(value)
        self.session.put(f"{self.base_url}/{key}", json=payload)


def encode(item: Sized) -> dict[str, "store.Value"]:
    return {"size": item.size(), "checksum": store.checksum(item)}


@log.catch
@retry(times=3)
def sync(repo: ItemRepository, items: list[Item]) -> int:
    count = 0
    for item in items:
        repo.put(item.name, item)
        count += 1
    print(len(items))
    return count
