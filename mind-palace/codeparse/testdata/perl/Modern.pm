use v5.38;
use experimental 'class';

package Zoo::Animal {
    use Moose;
    extends 'Zoo::Base';
    with 'Zoo::Role::Walks', 'Zoo::Role::Eats';

    has 'name' => (is => 'ro', isa => 'Str');
    has legs => (is => 'rw', default => 4);

    # Says hello, with a signature.
    sub greet ($self, $whom = 'world') {
        return sprintf('%s greets %s', $self->name, $whom);
    }

    __PACKAGE__->meta->make_immutable;
}

=head2 Zoo::Point

A point, written with the 5.38 class syntax.

=cut

class Zoo::Point 1.0 :isa(Zoo::Shape) {
    field $x :param = 0;
    field @history;

    method norm {
        return sqrt($x * $x);
    }

    method move ($dx) { $x += $dx; push @history, $dx; return $self }
}

1;
