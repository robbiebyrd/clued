"""Every way a module imports another."""
import os
import os.path
import sys as system, json
import xml.etree.ElementTree as ET
from collections import OrderedDict, defaultdict
from collections.abc import (
    Mapping,
    Sequence as Seq,
)
from . import sibling
from .. import parent_mod as pm
from .models import Item, make_item
from ..pkg.sub import helper
from typing import *
from __future__ import annotations

try:
    import ujson as fastjson
except ImportError:
    import json as fastjson

if TYPE_CHECKING:
    from .service import Service


def lazy():
    import numpy as np
    from pandas import DataFrame

    return np.array([]), DataFrame()
