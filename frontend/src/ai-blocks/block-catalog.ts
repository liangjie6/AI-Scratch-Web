import type {InputKind, TargetKind} from './program-ir';

export type BlockShape = 'hat' | 'stack' | 'cap' | 'reporter' | 'boolean';
export type FieldResolverKind = 'variable' | 'list' | 'broadcast';

export interface InputSpec {
  kind: InputKind | 'boolean' | 'any';
  required?: boolean;
  shadow?: Exclude<InputKind, 'block'>;
  shadowOpcode?: string;
  shadowField?: string;
  shadowResolver?: FieldResolverKind;
}

export interface FieldSpec {
  required?: boolean;
  resolver?: FieldResolverKind;
}

export interface BlockSpec {
  shape: BlockShape;
  targets: readonly TargetKind[];
  inputs?: Readonly<Record<string, InputSpec>>;
  fields?: Readonly<Record<string, FieldSpec>>;
  substacks?: readonly ('SUBSTACK' | 'SUBSTACK2')[];
}

const both = ['stage', 'sprite'] as const;
const sprite = ['sprite'] as const;
const input = (kind: InputSpec['kind'], shadow?: InputSpec['shadow']): InputSpec => ({kind, shadow, required: true});
const fields = (...names: string[]): Record<string, FieldSpec> => Object.fromEntries(names.map(name => [name, {required: true}]));

export const BLOCK_CATALOG = {
  event_whenflagclicked: {shape: 'hat', targets: both},
  event_whenkeypressed: {shape: 'hat', targets: both, fields: fields('KEY_OPTION')},
  event_whenthisspriteclicked: {shape: 'hat', targets: sprite},
  event_whenstageclicked: {shape: 'hat', targets: ['stage']},
  event_whenbackdropswitchesto: {shape: 'hat', targets: both, fields: fields('BACKDROP')},
  event_whengreaterthan: {shape: 'hat', targets: both, fields: fields('WHENGREATERTHANMENU'), inputs: {VALUE: input('number', 'number')}},
  event_whenbroadcastreceived: {shape: 'hat', targets: both, fields: {BROADCAST_OPTION: {required: true, resolver: 'broadcast'}}},
  event_broadcast: {shape: 'stack', targets: both, inputs: {BROADCAST_INPUT: {kind: 'text', required: true, shadow: 'text', shadowOpcode: 'event_broadcast_menu', shadowField: 'BROADCAST_OPTION', shadowResolver: 'broadcast'}}},
  event_broadcastandwait: {shape: 'stack', targets: both, inputs: {BROADCAST_INPUT: {kind: 'text', required: true, shadow: 'text', shadowOpcode: 'event_broadcast_menu', shadowField: 'BROADCAST_OPTION', shadowResolver: 'broadcast'}}},

  motion_movesteps: {shape: 'stack', targets: sprite, inputs: {STEPS: input('number', 'number')}},
  motion_turnright: {shape: 'stack', targets: sprite, inputs: {DEGREES: input('number', 'number')}},
  motion_turnleft: {shape: 'stack', targets: sprite, inputs: {DEGREES: input('number', 'number')}},
  motion_goto: {shape: 'stack', targets: sprite, inputs: {TO: {kind: 'text', required: true, shadow: 'text', shadowOpcode: 'motion_goto_menu', shadowField: 'TO'}}},
  motion_gotoxy: {shape: 'stack', targets: sprite, inputs: {X: input('number', 'number'), Y: input('number', 'number')}},
  motion_glideto: {shape: 'stack', targets: sprite, inputs: {SECS: input('number', 'number'), TO: {kind: 'text', required: true, shadow: 'text', shadowOpcode: 'motion_glideto_menu', shadowField: 'TO'}}},
  motion_glidesecstoxy: {shape: 'stack', targets: sprite, inputs: {SECS: input('number', 'number'), X: input('number', 'number'), Y: input('number', 'number')}},
  motion_pointindirection: {shape: 'stack', targets: sprite, inputs: {DIRECTION: input('angle', 'angle')}},
  motion_pointtowards: {shape: 'stack', targets: sprite, inputs: {TOWARDS: {kind: 'text', required: true, shadow: 'text', shadowOpcode: 'motion_pointtowards_menu', shadowField: 'TOWARDS'}}},
  motion_changexby: {shape: 'stack', targets: sprite, inputs: {DX: input('number', 'number')}},
  motion_setx: {shape: 'stack', targets: sprite, inputs: {X: input('number', 'number')}},
  motion_changeyby: {shape: 'stack', targets: sprite, inputs: {DY: input('number', 'number')}},
  motion_sety: {shape: 'stack', targets: sprite, inputs: {Y: input('number', 'number')}},
  motion_ifonedgebounce: {shape: 'stack', targets: sprite},
  motion_setrotationstyle: {shape: 'stack', targets: sprite, fields: fields('STYLE')},
  motion_xposition: {shape: 'reporter', targets: sprite},
  motion_yposition: {shape: 'reporter', targets: sprite},
  motion_direction: {shape: 'reporter', targets: sprite},

  looks_sayforsecs: {shape: 'stack', targets: sprite, inputs: {MESSAGE: input('text', 'text'), SECS: input('number', 'number')}},
  looks_say: {shape: 'stack', targets: sprite, inputs: {MESSAGE: input('text', 'text')}},
  looks_thinkforsecs: {shape: 'stack', targets: sprite, inputs: {MESSAGE: input('text', 'text'), SECS: input('number', 'number')}},
  looks_think: {shape: 'stack', targets: sprite, inputs: {MESSAGE: input('text', 'text')}},
  looks_switchcostumeto: {shape: 'stack', targets: sprite, inputs: {COSTUME: {kind: 'text', required: true, shadow: 'text', shadowOpcode: 'looks_costume', shadowField: 'COSTUME'}}},
  looks_nextcostume: {shape: 'stack', targets: sprite},
  looks_switchbackdropto: {shape: 'stack', targets: both, inputs: {BACKDROP: {kind: 'text', required: true, shadow: 'text', shadowOpcode: 'looks_backdrops', shadowField: 'BACKDROP'}}},
  looks_nextbackdrop: {shape: 'stack', targets: both},
  looks_changesizeby: {shape: 'stack', targets: sprite, inputs: {CHANGE: input('number', 'number')}},
  looks_setsizeto: {shape: 'stack', targets: sprite, inputs: {SIZE: input('number', 'number')}},
  looks_changeeffectby: {shape: 'stack', targets: both, fields: fields('EFFECT'), inputs: {CHANGE: input('number', 'number')}},
  looks_seteffectto: {shape: 'stack', targets: both, fields: fields('EFFECT'), inputs: {VALUE: input('number', 'number')}},
  looks_cleargraphiceffects: {shape: 'stack', targets: both},
  looks_show: {shape: 'stack', targets: sprite},
  looks_hide: {shape: 'stack', targets: sprite},
  looks_gotofrontback: {shape: 'stack', targets: sprite, fields: fields('FRONT_BACK')},
  looks_goforwardbackwardlayers: {shape: 'stack', targets: sprite, fields: fields('FORWARD_BACKWARD'), inputs: {NUM: input('number', 'number')}},
  looks_costumenumbername: {shape: 'reporter', targets: sprite, fields: fields('NUMBER_NAME')},
  looks_backdropnumbername: {shape: 'reporter', targets: both, fields: fields('NUMBER_NAME')},
  looks_size: {shape: 'reporter', targets: sprite},

  sound_playuntildone: {shape: 'stack', targets: both, inputs: {SOUND_MENU: {kind: 'text', required: true, shadow: 'text', shadowOpcode: 'sound_sounds_menu', shadowField: 'SOUND_MENU'}}},
  sound_play: {shape: 'stack', targets: both, inputs: {SOUND_MENU: {kind: 'text', required: true, shadow: 'text', shadowOpcode: 'sound_sounds_menu', shadowField: 'SOUND_MENU'}}},
  sound_stopallsounds: {shape: 'stack', targets: both},
  sound_changeeffectby: {shape: 'stack', targets: both, fields: fields('EFFECT'), inputs: {VALUE: input('number', 'number')}},
  sound_seteffectto: {shape: 'stack', targets: both, fields: fields('EFFECT'), inputs: {VALUE: input('number', 'number')}},
  sound_cleareffects: {shape: 'stack', targets: both},
  sound_changevolumeby: {shape: 'stack', targets: both, inputs: {VOLUME: input('number', 'number')}},
  sound_setvolumeto: {shape: 'stack', targets: both, inputs: {VOLUME: input('number', 'number')}},
  sound_volume: {shape: 'reporter', targets: both},

  control_wait: {shape: 'stack', targets: both, inputs: {DURATION: input('number', 'number')}},
  control_repeat: {shape: 'stack', targets: both, inputs: {TIMES: input('number', 'number')}, substacks: ['SUBSTACK']},
  control_forever: {shape: 'cap', targets: both, substacks: ['SUBSTACK']},
  control_if: {shape: 'stack', targets: both, inputs: {CONDITION: input('boolean')}, substacks: ['SUBSTACK']},
  control_if_else: {shape: 'stack', targets: both, inputs: {CONDITION: input('boolean')}, substacks: ['SUBSTACK', 'SUBSTACK2']},
  control_wait_until: {shape: 'stack', targets: both, inputs: {CONDITION: input('boolean')}},
  control_repeat_until: {shape: 'stack', targets: both, inputs: {CONDITION: input('boolean')}, substacks: ['SUBSTACK']},
  control_stop: {shape: 'cap', targets: both, fields: fields('STOP_OPTION')},
  control_start_as_clone: {shape: 'hat', targets: sprite},
  control_create_clone_of: {shape: 'stack', targets: both, inputs: {CLONE_OPTION: {kind: 'text', required: true, shadow: 'text', shadowOpcode: 'control_create_clone_of_menu', shadowField: 'CLONE_OPTION'}}},
  control_delete_this_clone: {shape: 'cap', targets: sprite},

  sensing_touchingobject: {shape: 'boolean', targets: sprite, inputs: {TOUCHINGOBJECTMENU: {kind: 'text', required: true, shadow: 'text', shadowOpcode: 'sensing_touchingobjectmenu', shadowField: 'TOUCHINGOBJECTMENU'}}},
  sensing_touchingcolor: {shape: 'boolean', targets: sprite, inputs: {COLOR: input('color', 'color')}},
  sensing_coloristouchingcolor: {shape: 'boolean', targets: sprite, inputs: {COLOR: input('color', 'color'), COLOR2: input('color', 'color')}},
  sensing_distanceto: {shape: 'reporter', targets: sprite, inputs: {DISTANCETOMENU: {kind: 'text', required: true, shadow: 'text', shadowOpcode: 'sensing_distancetomenu', shadowField: 'DISTANCETOMENU'}}},
  sensing_askandwait: {shape: 'stack', targets: both, inputs: {QUESTION: input('text', 'text')}},
  sensing_answer: {shape: 'reporter', targets: both},
  sensing_keypressed: {shape: 'boolean', targets: both, inputs: {KEY_OPTION: {kind: 'text', required: true, shadow: 'text', shadowOpcode: 'sensing_keyoptions', shadowField: 'KEY_OPTION'}}},
  sensing_mousedown: {shape: 'boolean', targets: both},
  sensing_mousex: {shape: 'reporter', targets: both},
  sensing_mousey: {shape: 'reporter', targets: both},
  sensing_setdragmode: {shape: 'stack', targets: sprite, fields: fields('DRAG_MODE')},
  sensing_loudness: {shape: 'reporter', targets: both},
  sensing_timer: {shape: 'reporter', targets: both},
  sensing_resettimer: {shape: 'stack', targets: both},
  sensing_current: {shape: 'reporter', targets: both, fields: fields('CURRENTMENU')},
  sensing_dayssince2000: {shape: 'reporter', targets: both},
  sensing_username: {shape: 'reporter', targets: both},

  operator_add: {shape: 'reporter', targets: both, inputs: {NUM1: input('number', 'number'), NUM2: input('number', 'number')}},
  operator_subtract: {shape: 'reporter', targets: both, inputs: {NUM1: input('number', 'number'), NUM2: input('number', 'number')}},
  operator_multiply: {shape: 'reporter', targets: both, inputs: {NUM1: input('number', 'number'), NUM2: input('number', 'number')}},
  operator_divide: {shape: 'reporter', targets: both, inputs: {NUM1: input('number', 'number'), NUM2: input('number', 'number')}},
  operator_random: {shape: 'reporter', targets: both, inputs: {FROM: input('number', 'number'), TO: input('number', 'number')}},
  operator_gt: {shape: 'boolean', targets: both, inputs: {OPERAND1: input('any', 'text'), OPERAND2: input('any', 'text')}},
  operator_lt: {shape: 'boolean', targets: both, inputs: {OPERAND1: input('any', 'text'), OPERAND2: input('any', 'text')}},
  operator_equals: {shape: 'boolean', targets: both, inputs: {OPERAND1: input('any', 'text'), OPERAND2: input('any', 'text')}},
  operator_and: {shape: 'boolean', targets: both, inputs: {OPERAND1: input('boolean'), OPERAND2: input('boolean')}},
  operator_or: {shape: 'boolean', targets: both, inputs: {OPERAND1: input('boolean'), OPERAND2: input('boolean')}},
  operator_not: {shape: 'boolean', targets: both, inputs: {OPERAND: input('boolean')}},
  operator_join: {shape: 'reporter', targets: both, inputs: {STRING1: input('text', 'text'), STRING2: input('text', 'text')}},
  operator_letter_of: {shape: 'reporter', targets: both, inputs: {LETTER: input('number', 'number'), STRING: input('text', 'text')}},
  operator_length: {shape: 'reporter', targets: both, inputs: {STRING: input('text', 'text')}},
  operator_contains: {shape: 'boolean', targets: both, inputs: {STRING1: input('text', 'text'), STRING2: input('text', 'text')}},
  operator_mod: {shape: 'reporter', targets: both, inputs: {NUM1: input('number', 'number'), NUM2: input('number', 'number')}},
  operator_round: {shape: 'reporter', targets: both, inputs: {NUM: input('number', 'number')}},
  operator_mathop: {shape: 'reporter', targets: both, fields: fields('OPERATOR'), inputs: {NUM: input('number', 'number')}},

  data_variable: {shape: 'reporter', targets: both, fields: {VARIABLE: {required: true, resolver: 'variable'}}},
  data_setvariableto: {shape: 'stack', targets: both, fields: {VARIABLE: {required: true, resolver: 'variable'}}, inputs: {VALUE: input('text', 'text')}},
  data_changevariableby: {shape: 'stack', targets: both, fields: {VARIABLE: {required: true, resolver: 'variable'}}, inputs: {VALUE: input('number', 'number')}},
  data_showvariable: {shape: 'stack', targets: both, fields: {VARIABLE: {required: true, resolver: 'variable'}}},
  data_hidevariable: {shape: 'stack', targets: both, fields: {VARIABLE: {required: true, resolver: 'variable'}}},
  data_listcontents: {shape: 'reporter', targets: both, fields: {LIST: {required: true, resolver: 'list'}}},
  data_addtolist: {shape: 'stack', targets: both, fields: {LIST: {required: true, resolver: 'list'}}, inputs: {ITEM: input('text', 'text')}},
  data_deleteoflist: {shape: 'stack', targets: both, fields: {LIST: {required: true, resolver: 'list'}}, inputs: {INDEX: input('text', 'text')}},
  data_deletealloflist: {shape: 'stack', targets: both, fields: {LIST: {required: true, resolver: 'list'}}},
  data_insertatlist: {shape: 'stack', targets: both, fields: {LIST: {required: true, resolver: 'list'}}, inputs: {ITEM: input('text', 'text'), INDEX: input('text', 'text')}},
  data_replaceitemoflist: {shape: 'stack', targets: both, fields: {LIST: {required: true, resolver: 'list'}}, inputs: {INDEX: input('text', 'text'), ITEM: input('text', 'text')}},
  data_itemoflist: {shape: 'reporter', targets: both, fields: {LIST: {required: true, resolver: 'list'}}, inputs: {INDEX: input('text', 'text')}},
  data_itemnumoflist: {shape: 'reporter', targets: both, fields: {LIST: {required: true, resolver: 'list'}}, inputs: {ITEM: input('text', 'text')}},
  data_lengthoflist: {shape: 'reporter', targets: both, fields: {LIST: {required: true, resolver: 'list'}}},
  data_listcontainsitem: {shape: 'boolean', targets: both, fields: {LIST: {required: true, resolver: 'list'}}, inputs: {ITEM: input('text', 'text')}},
  data_showlist: {shape: 'stack', targets: both, fields: {LIST: {required: true, resolver: 'list'}}},
  data_hidelist: {shape: 'stack', targets: both, fields: {LIST: {required: true, resolver: 'list'}}}
} as const satisfies Record<string, BlockSpec>;

export type NativeOpcode = keyof typeof BLOCK_CATALOG;

export const getBlockSpec = (opcode: string): BlockSpec | undefined =>
  (BLOCK_CATALOG as Record<string, BlockSpec>)[opcode];
