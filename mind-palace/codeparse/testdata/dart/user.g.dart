// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'user.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

User _$UserFromJson(Map<String, dynamic> json) => User(
      id: (json['id'] as num).toInt(),
      name: json['display_name'] as String,
      tags: (json['tags'] as List<dynamic>?)
              ?.map((e) => e as String)
              .toList() ??
          const [],
    );

Map<String, dynamic> _$UserToJson(User instance) => <String, dynamic>{
      'id': instance.id,
      'display_name': instance.name,
      'tags': instance.tags,
    };

extension UserCopy on User {
  User copyWith({int? id, String? name}) =>
      User(id: id ?? this.id, name: name ?? this.name, tags: tags);
}
