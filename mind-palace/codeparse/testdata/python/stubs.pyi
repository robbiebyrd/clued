"""Type stubs for the client module."""
from typing import Any, overload
from .models import Item

TIMEOUT: float
default_client: Client

class Client:
    base_url: str
    def __init__(self, base_url: str, *, timeout: float = ...) -> None: ...
    @overload
    def get(self, key: int) -> Item: ...
    @overload
    def get(self, key: str) -> Item | None: ...
    async def close(self) -> None: ...

def connect(url: str, **kwargs: Any) -> Client: ...
