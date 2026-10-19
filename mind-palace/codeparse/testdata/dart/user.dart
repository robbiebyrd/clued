import 'package:json_annotation/json_annotation.dart';

part 'user.g.dart';

/// A user of the service, read from and written to JSON.
@JsonSerializable(explicitToJson: true)
class User {
  User({required this.id, required this.name, this.tags = const []});

  /// Reads a user from [json].
  factory User.fromJson(Map<String, dynamic> json) => _$UserFromJson(json);

  final int id;

  /// The display name.
  @JsonKey(name: 'display_name')
  final String name;

  final List<String> tags;

  /// Writes the user as JSON.
  Map<String, dynamic> toJson() => _$UserToJson(this);
}
