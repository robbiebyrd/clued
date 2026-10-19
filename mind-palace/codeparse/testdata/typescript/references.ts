import { Base, Mixin, type Repository } from "./base";
import * as events from "node:events";

// References: calls, member calls, constructors, types, inheritance and
// decorators.

@Injectable()
export class UserService extends Base<User> implements Repository<User>, events.Listener {
  @Inject(TOKEN) private readonly db: Database;
  @Observable
  users: Map<string, User> = new Map();

  constructor(private readonly log: Logger) {
    super();
  }

  async find(id: string): Promise<User | undefined> {
    const row = await this.db.query<UserRow>(sql`select * from users where id = ${id}`);
    this.log.debug("find", id);
    return row ? toUser(row) : undefined;
  }

  save(user: User): void {
    validate(user);
    const copy = structuredClone(user);
    this.users.set(copy.id, copy);
    this.emitter?.emit("saved", copy);
  }
}

export class Admin extends Mixin(UserService) {}

export class Remote extends events.EventEmitter {}

export interface Paged<T> extends Iterable<T>, events.Disposable {
  page: number;
  next(): Paged<T> | null;
}

export function toUser(row: UserRow): User {
  return new User(row.id, row.name);
}

const emitter = new events.EventEmitter();
let pending: Promise<Array<User>> = Promise.resolve([]);
